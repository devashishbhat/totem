/**
 * Alerts in plain words.
 *
 * The server stores an exact title: "Untrusted memory chose `to` of
 * send_email". It is the right thing to keep in the record and the wrong thing
 * to put in front of someone reading their first alert, because every noun in
 * it is a term of art. These functions turn an alert into a sentence that says
 * what happened and why it matters, and keep the exact title for the detail
 * line, so nothing is hidden from a reader who wants it.
 */

import type { Alert } from "@/lib/api";

export type AlertCopy = {
  /** What happened, in a sentence anyone can read. */
  headline: string;
  /** Why it matters, or where the thing came from. */
  detail: string;
  /** A few words for a narrow list, where the headline would be cut off. */
  short: string;
};

type Evidence = {
  tool?: string;
  tier?: string;
  trust_min?: number;
  memory_key?: string;
  origin?: string;
  value_flow?: { arg_path?: string; memory_key?: string; origin?: string }[];
};

/** The last segment of a memory key, which is what tells memories apart. */
export function shortKey(key: string): string {
  const cut = key.lastIndexOf("/");
  return cut >= 0 ? key.slice(cut + 1) : key;
}

function evidenceOf(alert: Alert): Evidence {
  return (alert.evidence ?? {}) as Evidence;
}

/** Where untrusted text came from, named if we know, described if not. */
function origin(evidence: Evidence): string {
  const flow = (evidence.value_flow ?? []).find((f) => f.origin);
  const named = evidence.origin ?? flow?.origin;
  if (named && /^https?:\/\//.test(named)) return named.replace(/^https?:\/\//, "");
  if (named) return named;
  return "something it read";
}

export function describeAlert(alert: Alert): AlertCopy {
  const evidence = evidenceOf(alert);
  const tool = evidence.tool ?? "a tool";

  if (alert.kind === "taint.side_effect_from_untrusted") {
    const flow = (evidence.value_flow ?? []).find((f) => f.arg_path);
    const arg = flow?.arg_path;
    const memory = flow?.memory_key ? shortKey(flow.memory_key) : undefined;

    if (alert.severity === "critical") {
      // The argument name stays out of the headline. Written into a sentence,
      // a name like "to" reads as a preposition and the sentence falls apart.
      const value = arg ? `the value for "${arg}"` : "that value";
      return {
        headline: `Something the assistant read chose what ${tool} did`,
        detail: memory
          ? `You did not supply ${value}. It traces back through what the assistant remembered as ${memory}, to ${origin(evidence)}.`
          : `You did not supply ${value}. It traces back to ${origin(evidence)}.`,
        short: `${tool} used text from outside`,
      };
    }
    return {
      headline: `${tool} ran while text from outside was still in view`,
      detail: `The value it used did not come from there, but ${origin(evidence)} was in front of the model when it decided.`,
      short: `${tool} ran with outside text in view`,
    };
  }

  if (alert.kind === "memory.untrusted_contact_value") {
    const key = evidence.memory_key ? shortKey(evidence.memory_key) : "a memory";
    return {
      headline: "The assistant is remembering a contact detail you did not give it",
      detail: `It saved ${key} from ${origin(evidence)}. An address picked up this way is the shape a payment redirect takes.`,
      short: `Remembered a contact from outside`,
    };
  }

  return { headline: alert.title, detail: "", short: alert.title };
}

/** One line on what the assistant did, for a decision. */
export function describeDecision(tool: string, sideEffect: boolean): string {
  if (!tool) return "Replied to you";
  return sideEffect ? `Used ${tool}, which changes something` : `Used ${tool}`;
}

/** What a redaction placeholder is, in a word. */
const PLACEHOLDER_WORDS: Record<string, string> = {
  EMAIL: "an email address",
  PHONE: "a phone number",
  CARD: "a card number",
  KEY: "an API key",
  JWT: "a token",
  TOKEN: "a token",
};

/**
 * Text with its redaction placeholders written out as words.
 *
 * Most of the dashboard renders a placeholder as a chip, which carries the
 * digest and so lets a reader match one value to another. In a sidebar row or
 * a list there is no room for that, and a bare "[EMAIL:590e1d]" in the middle
 * of a sentence reads as damage.
 */
export function plainPlaceholders(text: string): string {
  return text.replace(
    /\[(EMAIL|PHONE|CARD|KEY|JWT|TOKEN):[0-9a-f]{6}\]/g,
    (_, kind: string) => PLACEHOLDER_WORDS[kind] ?? "a hidden value",
  );
}
