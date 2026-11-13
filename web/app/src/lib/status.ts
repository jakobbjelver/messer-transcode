// One table mapping a file's state to the family's status badge. Rule 8: status
// is icon + label and colour only reinforces, so an in-flight job is `neutral`
// with its own label — an amber warning triangle would assert a problem the
// pipeline does not have. "Live" is carried by the moving progress bar, not by
// a colour that means something else.

export type BadgeStatus = "success" | "warning" | "danger" | "neutral";

export type StatePresentation = { status: BadgeStatus; label: string };

const STATES: Record<string, StatePresentation> = {
  planned: { status: "neutral", label: "Planned" },
  encoding: { status: "neutral", label: "Encoding" },
  verifying: { status: "neutral", label: "Verifying" },
  replacing: { status: "neutral", label: "Replacing" },
  done: { status: "success", label: "Done" },
  skipped: { status: "neutral", label: "Left alone" },
  // Not a failure: the file left the library (Sonarr upgraded it, or it was
  // deleted) and the scan retired its row. Neutral, because a queue that cries
  // wolf over a deleted file teaches you to ignore the one that matters.
  gone: { status: "neutral", label: "Gone from disk" },
  failed: { status: "danger", label: "Failed" },
};

export function statePresentation(state: string): StatePresentation {
  return STATES[state] ?? { status: "neutral", label: state || "Unknown" };
}

export function isRunning(state: string): boolean {
  return state === "encoding" || state === "verifying" || state === "replacing";
}
