package replay

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/devashishbhat/totem/server/internal/store"
)

func payload(messages ...map[string]any) map[string]any {
	items := make([]any, 0, len(messages))
	for _, m := range messages {
		items = append(items, m)
	}
	return map[string]any{"model": "gpt-4o-mini", "messages": items}
}

func contentOf(t *testing.T, p map[string]any, index int) string {
	t.Helper()
	messages, ok := p["messages"].([]any)
	if !ok || index >= len(messages) {
		t.Fatalf("no message %d in %v", index, p)
	}
	message, _ := messages[index].(map[string]any)
	content, _ := message["content"].(string)
	return content
}

func TestEditPayloadCutsTheSpanOut(t *testing.T) {
	memory := "Invoices for Acme go to billing@acme-payments.example"
	line := "Remembered: " + memory + " (noted last week)"
	p := payload(
		map[string]any{"role": "system", "content": "You are an assistant."},
		map[string]any{"role": "user", "content": line},
	)
	start := len([]rune("Remembered: "))
	edited, removed, err := editPayload(p, []Span{{1, start, start + len([]rune(memory))}})
	if err != nil {
		t.Fatalf("editPayload: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed %d spans, want 1", removed)
	}
	if got := contentOf(t, edited, 1); got != "Remembered:  (noted last week)" {
		t.Fatalf("edited message is %q", got)
	}
	// The original must be untouched: the baseline arm resends it.
	if contentOf(t, p, 1) != line {
		t.Fatal("editPayload modified the original payload")
	}
}

func TestASpanCoveringALineTakesTheWholeLine(t *testing.T) {
	// Half a sentence left in the prompt would itself change the answer.
	memory := "Invoices for Acme go to billing@acme-payments.example"
	content := "Notes:\n- " + memory + "\n- Acme pays on net 30.\n"
	start := len([]rune("Notes:\n- "))
	p := payload(map[string]any{"role": "system", "content": content})
	edited, removed, err := editPayload(p, []Span{{0, start, start + len([]rune(memory))}})
	if err != nil {
		t.Fatalf("editPayload: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed %d spans", removed)
	}
	// The rest of the prompt, trailing newline included, is left as it was.
	if got := contentOf(t, edited, 0); got != "Notes:\n- Acme pays on net 30.\n" {
		t.Fatalf("edited content is %q", got)
	}
}

func TestEditPayloadIgnoresSpansItCannotPlace(t *testing.T) {
	p := payload(map[string]any{"role": "user", "content": "hello"})
	for _, spans := range [][]Span{
		{{5, 0, 3}},  // no such message
		{{0, 3, 3}},  // empty range
		{{0, 9, 12}}, // past the end
	} {
		edited, removed, err := editPayload(p, spans)
		if err != nil {
			t.Fatalf("editPayload(%v): %v", spans, err)
		}
		if removed != 0 || contentOf(t, edited, 0) != "hello" {
			t.Fatalf("spans %v changed %q (removed %d)", spans, contentOf(t, edited, 0), removed)
		}
	}
}

func TestEditPayloadNeedsMessages(t *testing.T) {
	if _, _, err := editPayload(map[string]any{"model": "x"}, []Span{{0, 0, 1}}); err == nil {
		t.Fatal("expected an error when there are no messages")
	}
}

func TestEditPayloadLeavesMultiPartContentAlone(t *testing.T) {
	// There is no single offset space to cut from, so the span is skipped
	// rather than guessed at.
	p := payload(map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": "hello"},
	}})
	_, removed, err := editPayload(p, []Span{{0, 0, 3}})
	if err != nil || removed != 0 {
		t.Fatalf("removed %d, err %v", removed, err)
	}
}

func TestOverlappingSpansAreMergedOnce(t *testing.T) {
	content := "alpha beta gamma delta"
	p := payload(map[string]any{"role": "user", "content": content})
	edited, removed, err := editPayload(p, []Span{{0, 0, 10}, {0, 6, 16}})
	if err != nil {
		t.Fatalf("editPayload: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed %d ranges, want 1 merged range", removed)
	}
	if got := contentOf(t, edited, 0); got != " delta" {
		t.Fatalf("edited content is %q", got)
	}
}

func TestOutputKeyIgnoresArgumentOrder(t *testing.T) {
	a := Output{Tool: "send_email", Args: json.RawMessage(`{"to":"x@y.example","body":"hi"}`)}
	b := Output{Tool: "send_email", Args: json.RawMessage(`{"body":"hi","to":"x@y.example"}`)}
	if outputKey(a, nil) != outputKey(b, nil) {
		t.Fatalf("key %q != %q", outputKey(a, nil), outputKey(b, nil))
	}
	c := Output{Tool: "send_email", Args: json.RawMessage(`{"to":"other@y.example","body":"hi"}`)}
	if outputKey(a, nil) == outputKey(c, nil) {
		t.Fatal("a different address produced the same key")
	}
}

// The decision is the address the memory supplied, not the prose the model
// wraps around it. A model asked the same question twice writes a different
// email body both times, and counting that as a changed decision made every
// baseline sample disagree with every other one.
func TestOutputKeyComparesOnlyTheFedArguments(t *testing.T) {
	recorded := Output{Tool: "send_email", Args: json.RawMessage(
		`{"to":"ap@acme.example","body":"Please find attached the October invoice."}`)}
	reworded := Output{Tool: "send_email", Args: json.RawMessage(
		`{"to":"ap@acme.example","body":"Dear Acme Team,\n\nInvoice attached.\n\nFinance"}`)}
	if outputKey(recorded, []string{"to"}) != outputKey(reworded, []string{"to"}) {
		t.Fatal("a reworded body counted as a different decision")
	}
	if outputKey(recorded, nil) == outputKey(reworded, nil) {
		t.Fatal("without paths the whole argument object must still count")
	}

	elsewhere := Output{Tool: "send_email", Args: json.RawMessage(
		`{"to":"someone@else.example","body":"Please find attached the October invoice."}`)}
	if outputKey(recorded, []string{"to"}) == outputKey(elsewhere, []string{"to"}) {
		t.Fatal("a different address must count as a different decision")
	}

	// A model that drops the argument altogether decided something else.
	dropped := Output{Tool: "send_email", Args: json.RawMessage(`{"body":"hi"}`)}
	if outputKey(recorded, []string{"to"}) == outputKey(dropped, []string{"to"}) {
		t.Fatal("a missing argument produced the same key")
	}

	// A different tool is a different decision whatever the paths say.
	other := Output{Tool: "lookup_contact", Args: json.RawMessage(`{"to":"ap@acme.example"}`)}
	if outputKey(recorded, []string{"to"}) == outputKey(other, []string{"to"}) {
		t.Fatal("two tools produced the same key")
	}
}

func TestValueAtPathWalksObjectsAndArrays(t *testing.T) {
	var args any
	if err := json.Unmarshal([]byte(
		`{"message":{"body":"hi"},"recipients":["first@y.example","second@y.example"]}`,
	), &args); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cases := map[string]any{
		"message.body":    "hi",
		"recipients[1]":   "second@y.example",
		"recipients[9]":   nil,
		"message.missing": nil,
		"message.body.no": nil,
	}
	for path, want := range cases {
		if got := valueAtPath(args, path); got != want {
			t.Errorf("valueAtPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestValueFlowPathsAreTheMemorysOwn(t *testing.T) {
	mine, theirs := uuid.New(), uuid.New()
	attributions := []store.Attribution{
		{Level: "value_flow", MemoryVersionID: &mine, Detail: json.RawMessage(`{"arg_path":"to"}`)},
		// The same path twice must not be listed twice.
		{Level: "value_flow", MemoryVersionID: &mine, Detail: json.RawMessage(`{"arg_path":"to"}`)},
		{Level: "value_flow", MemoryVersionID: &mine, Detail: json.RawMessage(`{"arg_path":"cc"}`)},
		// Another memory's flow, a weaker level, and a source-only flow are
		// none of this experiment's business.
		{Level: "value_flow", MemoryVersionID: &theirs, Detail: json.RawMessage(`{"arg_path":"body"}`)},
		{Level: "in_context", MemoryVersionID: &mine, Detail: json.RawMessage(`{"spans":[[0,0,1]]}`)},
		{Level: "value_flow", Detail: json.RawMessage(`{"arg_path":"url"}`)},
	}
	got := valueFlowPaths(attributions, mine)
	if len(got) != 2 || got[0] != "cc" || got[1] != "to" {
		t.Fatalf("paths are %v, want [cc to]", got)
	}
	if valueFlowPaths(nil, mine) != nil {
		t.Fatal("no attributions should give no paths")
	}
}

func TestModalOutputIsTheMostCommonBaseline(t *testing.T) {
	real := Output{Tool: "send_email", Args: json.RawMessage(`{"to":"ap@acme.example"}`)}
	odd := Output{Tool: "send_email", Args: json.RawMessage(`{"to":"someone@else.example"}`)}
	samples := []Output{real, real, odd, real}
	if modalOutput(samples, nil) != outputKey(real, nil) {
		t.Fatalf("modal output is %q", modalOutput(samples, nil))
	}
	if modalOutput(nil, nil) != "" {
		t.Fatal("no samples should give no reference")
	}
}

func TestSamplesFor(t *testing.T) {
	cases := []struct {
		requested int
		zeroTemp  bool
		want      int
	}{
		{0, false, defaultSamples},
		{0, true, deterministicSample},
		{5, false, 5},
		{100, false, maxSamples},
		{-3, true, deterministicSample},
	}
	for _, c := range cases {
		if got := samplesFor(c.requested, c.zeroTemp); got != c.want {
			t.Errorf("samplesFor(%d, %v) = %d, want %d", c.requested, c.zeroTemp, got, c.want)
		}
	}
}
