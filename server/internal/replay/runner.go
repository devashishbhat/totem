// Package replay proves influence by re-running one recorded model call with a
// memory removed from the prompt and comparing what the agent decides.
//
// Replay never executes tools. It only asks the model what it would do.
package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/devashishbhat/totem/server/internal/config"
	"github.com/devashishbhat/totem/server/internal/event"
	"github.com/devashishbhat/totem/server/internal/store"
	"github.com/devashishbhat/totem/server/internal/totemtext"
)

// Tunables.
const (
	defaultSamples      = 8
	deterministicSample = 3
	maxSamples          = 16
	// Two at a time. Replay is not latency-sensitive, and a modest provider
	// tier allows only a handful of requests per minute - a burst of eight
	// buys nothing and costs the whole experiment.
	maxConcurrency    = 2
	experimentTimeout = 10 * time.Minute
	// How many times a request waits out a rate limit before giving up.
	rateLimitRetries = 8
)

// Runner starts and executes replay experiments.
type Runner struct {
	db     *store.Store
	cfg    config.Config
	log    *slog.Logger
	client ChatClient
	wg     sync.WaitGroup
}

// ChatClient is the one model operation replay needs. Tests substitute a fake,
// which is how the package stays testable with no network.
type ChatClient interface {
	Complete(ctx context.Context, payload map[string]any) (Output, error)
}

// Output is one sampled decision.
type Output struct {
	Tool    string          `json:"tool,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
	Content string          `json:"content,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// New builds a runner that talks to OpenAI.
func New(db *store.Store, cfg config.Config, log *slog.Logger) *Runner {
	var client ChatClient
	if cfg.OpenAIKey != "" {
		client = &openAIClient{
			// A rate limit is the expected answer on a small account, not a
			// failure: the SDK waits for the provider's retry-after and tries
			// again rather than losing the arm.
			client: openai.NewClient(
				option.WithAPIKey(cfg.OpenAIKey),
				option.WithMaxRetries(rateLimitRetries),
			),
			log: log,
		}
	}
	return &Runner{db: db, cfg: cfg, log: log, client: client}
}

// NewWithClient builds a runner over a given client, for tests.
func NewWithClient(db *store.Store, cfg config.Config, log *slog.Logger, client ChatClient) *Runner {
	return &Runner{db: db, cfg: cfg, log: log, client: client}
}

// Wait blocks until running experiments finish. Used on shutdown and in tests.
func (r *Runner) Wait() { r.wg.Wait() }

// Start validates the request, records the experiment and runs it in the
// background. It returns the experiment id.
func (r *Runner) Start(ctx context.Context, decisionID, versionID uuid.UUID, samplesPerArm int) (uuid.UUID, error) {
	if r.cfg.DemoMode {
		return uuid.Nil, errors.New("replays are disabled on the read-only demo")
	}
	if r.client == nil {
		return uuid.Nil, errors.New("OPENAI_API_KEY is not set on the server, so replay cannot call the model")
	}
	d, err := store.DecisionByID(ctx, r.db.Pool, decisionID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("decision: %w", err)
	}
	if d.LLMCallID == nil {
		return uuid.Nil, errors.New("this decision has no recorded model call to replay")
	}
	call, err := store.LLMCallByID(ctx, r.db.Pool, *d.LLMCallID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("model call: %w", err)
	}
	if len(call.Payload) == 0 || string(call.Payload) == "{}" {
		return uuid.Nil, errors.New("the model call was recorded without its payload, so it cannot be replayed")
	}
	version, err := store.MemoryVersionByID(ctx, r.db.Pool, versionID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("memory version: %w", err)
	}
	// Which arguments this memory supplied a value to. Without them the
	// comparison falls back to the whole argument object, which a free-text
	// argument such as an email body makes meaningless.
	attributions, err := store.AttributionsOf(ctx, r.db.Pool, d.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("attributions: %w", err)
	}
	plan, err := buildPlan(d, call, version, valueFlowPaths(attributions, version.ID))
	if err != nil {
		return uuid.Nil, err
	}

	samples := samplesFor(samplesPerArm, plan.temperatureZero)
	exp := store.ReplayExperiment{
		ID:                    uuid.New(),
		DecisionID:            d.ID,
		TargetMemoryVersionID: version.ID,
		SamplesPerArm:         samples,
		Model:                 plan.model,
		Status:                store.ReplayPending,
		CreatedAt:             time.Now().UTC(),
	}
	if err := r.db.CreateReplay(ctx, exp); err != nil {
		return uuid.Nil, err
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), experimentTimeout)
		defer cancel()
		if err := r.run(runCtx, exp, plan); err != nil {
			r.log.Error("replay failed", "experiment", exp.ID, "err", err)
			_ = r.db.SetReplayStatus(context.WithoutCancel(ctx), exp.ID, store.ReplayFailed, err.Error())
		}
	}()
	return exp.ID, nil
}

// plan is everything the two arms need, computed once up front.
type plan struct {
	decisionID uuid.UUID
	versionID  uuid.UUID
	// decisionKey is the recorded decision, in the same form a sample takes,
	// so the baseline can be checked against it.
	decisionKey string
	// argPaths are the arguments the target memory supplied a value to, as
	// recorded value-flow attributions. Comparison is restricted to these,
	// because they are what the memory decided.
	argPaths        []string
	model           string
	baseline        map[string]any
	variant         map[string]any
	temperatureZero bool
	spansRemoved    int
}

// buildPlan reads the recorded request and produces the edited copy with the
// target memory's text cut out of the prompt.
func buildPlan(d store.Decision, call store.LLMCall, version store.MemoryVersion, argPaths []string) (plan, error) {
	var payload map[string]any
	if err := json.Unmarshal(call.Payload, &payload); err != nil {
		return plan{}, fmt.Errorf("recorded payload is not an object: %w", err)
	}
	// Replay asks what the model would decide; it must not stream.
	delete(payload, "stream")

	var refs []event.InContextRef
	if len(call.InContext) > 0 {
		if err := json.Unmarshal(call.InContext, &refs); err != nil {
			return plan{}, fmt.Errorf("recorded in_context is unreadable: %w", err)
		}
	}
	var spans []Span
	for _, ref := range refs {
		if ref.RefType != "memory" || ref.HMAC != version.HMAC {
			continue
		}
		for _, s := range ref.Spans {
			spans = append(spans, Span(s))
		}
	}
	if len(spans) == 0 {
		return plan{}, errors.New("that memory was not matched in this prompt, so there is nothing to remove")
	}
	variant, removed, err := editPayload(payload, spans)
	if err != nil {
		return plan{}, err
	}
	if removed == 0 {
		return plan{}, errors.New("the recorded spans did not line up with the prompt text")
	}
	baseline, err := deepCopy(payload)
	if err != nil {
		return plan{}, err
	}

	model, _ := payload["model"].(string)
	if model == "" {
		model = call.Model
	}
	if model == "" {
		// The scripted demo model does not record one, and neither does a
		// provider the SDK could not read a name from. Saying so here costs
		// nothing; finding out from a provider's 400 costs an experiment.
		return plan{}, errors.New(
			"the recorded call did not say which model it used, so it cannot be replayed. " +
				"A run recorded with the scripted demo model cannot be replayed: " +
				"run the agent against a real model first")
	}
	temp, hasTemp := payload["temperature"].(float64)
	return plan{
		decisionID:      d.ID,
		decisionKey:     outputKey(Output{Tool: d.Tool, Args: d.Args}, argPaths),
		argPaths:        argPaths,
		versionID:       version.ID,
		model:           model,
		baseline:        baseline,
		variant:         variant,
		temperatureZero: hasTemp && temp == 0,
		spansRemoved:    removed,
	}, nil
}

func samplesFor(requested int, temperatureZero bool) int {
	n := requested
	if n <= 0 {
		n = defaultSamples
		if temperatureZero {
			n = deterministicSample
		}
	}
	if n > maxSamples {
		n = maxSamples
	}
	return n
}

func (r *Runner) run(ctx context.Context, exp store.ReplayExperiment, p plan) error {
	if err := r.db.SetReplayStatus(ctx, exp.ID, store.ReplayRunning, ""); err != nil {
		return err
	}
	baseline := r.sampleArm(ctx, p.baseline, exp.SamplesPerArm)
	variant := r.sampleArm(ctx, p.variant, exp.SamplesPerArm)

	// If a whole arm failed, the model never answered, and a verdict computed
	// from errors would read as if it had.
	if err := armFailure(baseline, variant); err != nil {
		return err
	}

	// What the model just said is as sensitive as what it was told. Redacting
	// here means a verbatim prompt never leaks a value into replay_samples,
	// and it puts the samples in the same form as the recorded decision, so
	// the two can be compared.
	for i := range baseline {
		baseline[i] = r.redactOutput(baseline[i])
	}
	for i := range variant {
		variant[i] = r.redactOutput(variant[i])
	}

	reference := modalOutput(baseline, p.argPaths)

	baseFlips := 0
	for i, out := range baseline {
		matches := outputKey(out, p.argPaths) == reference
		if !matches {
			baseFlips++
		}
		if err := r.db.InsertReplaySample(ctx, store.ReplaySample{
			ExperimentID: exp.ID, Arm: "baseline", Idx: i,
			Output: mustJSON(out), MatchesBaseline: matches}); err != nil {
			return err
		}
	}
	variantFlips := 0
	for i, out := range variant {
		matches := outputKey(out, p.argPaths) == reference
		if !matches {
			variantFlips++
		}
		if err := r.db.InsertReplaySample(ctx, store.ReplaySample{
			ExperimentID: exp.ID, Arm: "variant", Idx: i,
			Output: mustJSON(out), MatchesBaseline: matches}); err != nil {
			return err
		}
	}

	// Only now, with the samples on record, is it worth asking whether this
	// experiment can mean anything. A baseline that does not reproduce the
	// recorded decision is not a baseline: without this check the experiment
	// would measure the difference between two things that both differ from
	// what the agent actually did.
	if reference != p.decisionKey {
		return fmt.Errorf(
			"the baseline did not reproduce the recorded decision: it chose %s, the agent chose %s. %s",
			describeKey(reference), describeKey(p.decisionKey), reproductionHint(r.cfg.RedactPayloads))
	}

	diff := NewcombeDifference(baseFlips, len(baseline), variantFlips, len(variant))
	verdict := Verdict(diff)
	if err := r.db.FinishReplay(ctx, exp.ID, store.ReplayResult{
		BaselineFlip: diff.BaselineFlip,
		VariantFlip:  diff.VariantFlip,
		Effect:       diff.Effect,
		CILow:        diff.CILow,
		CIHigh:       diff.CIHigh,
		Verdict:      verdict,
		Model:        p.model,
	}); err != nil {
		return err
	}
	r.log.Info("replay finished", "experiment", exp.ID, "verdict", verdict,
		"effect", diff.Effect, "ci_low", diff.CILow, "ci_high", diff.CIHigh)

	if verdict == store.VerdictStrong || verdict == store.VerdictModerate {
		return r.writeCounterfactual(ctx, exp, diff, verdict, variantFlips, len(variant))
	}
	return nil
}

// sampleArm asks the model the same question N times, with a small amount of
// concurrency so an eight-sample arm does not take a minute.
func (r *Runner) sampleArm(ctx context.Context, payload map[string]any, n int) []Output {
	out := make([]Output, n)
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := r.client.Complete(ctx, payload)
			if err != nil {
				out[i] = Output{Error: err.Error()}
				return
			}
			out[i] = res
		}(i)
	}
	wg.Wait()
	return out
}

// writeCounterfactual records that the replay proved influence, so the decision
// page can show it as an attribution level of its own.
func (r *Runner) writeCounterfactual(ctx context.Context, exp store.ReplayExperiment,
	diff Difference, verdict string, changed, total int) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	detail, err := json.Marshal(map[string]any{
		"experiment_id": exp.ID,
		"verdict":       verdict,
		"effect":        diff.Effect,
		"ci_low":        diff.CILow,
		"ci_high":       diff.CIHigh,
		"changed":       changed,
		"total":         total,
	})
	if err != nil {
		return err
	}
	versionID := exp.TargetMemoryVersionID
	if err := store.InsertAttribution(ctx, tx, store.Attribution{
		DecisionID:      exp.DecisionID,
		MemoryVersionID: &versionID,
		Level:           store.LevelCounterfactual,
		Detail:          detail,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// armFailure reports the model error when an entire arm failed.
func armFailure(baseline, variant []Output) error {
	for name, arm := range map[string][]Output{"baseline": baseline, "variant": variant} {
		if len(arm) == 0 {
			continue
		}
		failed := 0
		for _, out := range arm {
			if out.Error != "" {
				failed++
			}
		}
		if failed == len(arm) {
			return fmt.Errorf("every %s sample failed: %s", name, arm[0].Error)
		}
	}
	return nil
}

// redactOutput puts a sampled decision through the same redaction the rest
// of the pipeline applies, so it can be stored and compared safely.
func (r *Runner) redactOutput(o Output) Output {
	if o.Error != "" {
		return o
	}
	if len(o.Args) > 0 {
		args, _ := totemtext.Redact(r.cfg.ProjectSalt, string(o.Args))
		o.Args = json.RawMessage(args)
	}
	if o.Content != "" {
		o.Content, _ = totemtext.Redact(r.cfg.ProjectSalt, o.Content)
	}
	return o
}

// describeKey renders an output key for an error message.
func describeKey(key string) string {
	switch {
	case key == "":
		return "nothing"
	case strings.HasPrefix(key, "tool:"):
		parts := strings.SplitN(strings.TrimPrefix(key, "tool:"), ":", 2)
		if len(parts) == 2 {
			return fmt.Sprintf("%s(%s)", parts[0], parts[1])
		}
		return parts[0]
	case strings.HasPrefix(key, "text:"):
		return "a written answer"
	default:
		return key
	}
}

// reproductionHint names the usual cause. A prompt stored redacted cannot be
// replayed faithfully when the value that drove the decision was one of the
// things redacted out of it.
func reproductionHint(redactPayloads bool) string {
	if redactPayloads {
		return "The recorded prompt is redacted, so the model no longer sees the value it acted on. " +
			"Set TM_REDACT_PAYLOADS=false on the server and redact_payloads=False in the SDK to " +
			"record prompts verbatim, then run the agent again."
	}
	return "The model may simply be answering differently today."
}

// outputKey is the identity of a sampled decision: the tool it called and the
// canonical form of its arguments, or the text it answered with.
//
// When paths are given, only those arguments count. A decision is "the same"
// when the model called the same tool with the same value in the argument the
// memory fed. Everything else a model writes around that value, the body of
// an email above all, is prose it rewrites every time it is asked, and
// comparing it would report the model's wording as a changed decision.
func outputKey(o Output, paths []string) string {
	if o.Error != "" {
		return "error:" + o.Error
	}
	if o.Tool == "" {
		return "text:" + totemtext.Normalize(o.Content)
	}
	args, err := totemtext.Canonical(projectArgs(o.Args, paths))
	if err != nil {
		args = string(o.Args)
	}
	return "tool:" + o.Tool + ":" + args
}

// valueFlowPaths are the argument paths that one memory version supplied a
// value to, deduplicated and in a stable order.
func valueFlowPaths(attributions []store.Attribution, versionID uuid.UUID) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range attributions {
		if a.Level != "value_flow" || a.MemoryVersionID == nil || *a.MemoryVersionID != versionID {
			continue
		}
		var detail struct {
			ArgPath string `json:"arg_path"`
		}
		if err := json.Unmarshal(a.Detail, &detail); err != nil || detail.ArgPath == "" {
			continue
		}
		if !seen[detail.ArgPath] {
			seen[detail.ArgPath] = true
			out = append(out, detail.ArgPath)
		}
	}
	sort.Strings(out)
	return out
}

// projectArgs keeps only the named paths, as a flat object keyed by path. A
// path that an output does not have is kept as null, so a model that drops the
// argument altogether reads as a different decision rather than a missing one.
// With no paths it returns the arguments untouched.
func projectArgs(args json.RawMessage, paths []string) json.RawMessage {
	if len(paths) == 0 {
		return args
	}
	var parsed any
	if err := json.Unmarshal(args, &parsed); err != nil {
		return args
	}
	kept := make(map[string]any, len(paths))
	for _, p := range paths {
		kept[p] = valueAtPath(parsed, p)
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return args
	}
	return out
}

// valueAtPath walks a dotted path with bracketed indexes, the form the value
// flow matcher records: "to", "message.body", "recipients[0]".
func valueAtPath(value any, path string) any {
	for _, segment := range strings.Split(path, ".") {
		name, indexes := splitIndexes(segment)
		if name != "" {
			object, ok := value.(map[string]any)
			if !ok {
				return nil
			}
			value, ok = object[name]
			if !ok {
				return nil
			}
		}
		for _, i := range indexes {
			list, ok := value.([]any)
			if !ok || i < 0 || i >= len(list) {
				return nil
			}
			value = list[i]
		}
	}
	return value
}

// splitIndexes takes "recipients[0][1]" to ("recipients", [0, 1]).
func splitIndexes(segment string) (string, []int) {
	open := strings.Index(segment, "[")
	if open < 0 {
		return segment, nil
	}
	name := segment[:open]
	var indexes []int
	for rest := segment[open:]; rest != ""; {
		if !strings.HasPrefix(rest, "[") {
			return segment, nil
		}
		close := strings.Index(rest, "]")
		if close < 0 {
			return segment, nil
		}
		i, err := strconv.Atoi(rest[1:close])
		if err != nil {
			return segment, nil
		}
		indexes = append(indexes, i)
		rest = rest[close+1:]
	}
	return name, indexes
}

// modalOutput is the most common baseline output: the reference the arms are
// compared against.
func modalOutput(samples []Output, paths []string) string {
	counts := map[string]int{}
	best, bestN := "", -1
	for _, s := range samples {
		k := outputKey(s, paths)
		counts[k]++
		// Ties break on the first-seen key, which keeps the reference stable.
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	return best
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}
