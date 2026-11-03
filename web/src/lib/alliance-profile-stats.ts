import type { AllianceProfileDocument, AllianceProfileField } from "./types";

export type AllianceFieldBucket = "filled" | "pending" | "empty";

export type AlliancePhaseStats = {
  total: number;
  filled: number;
  pending: number;
  empty: number;
};

export type AllianceDocumentStats = AlliancePhaseStats & {
  byPhase: Record<number, AlliancePhaseStats>;
};

// fieldBucket implements the UI "有效已填" rule:
// - supplied/confirmed count only when they actually carry content;
// - not_applicable counts only when a reason is written;
// - unknown never counts, even when it carries a "待确认：" note.
export function fieldBucket(field: AllianceProfileField): AllianceFieldBucket {
  const value = (field.value ?? "").trim();
  if (field.status === "unknown") {
    return value ? "pending" : "empty";
  }
  if (field.status === "not_applicable") {
    return value ? "filled" : "empty";
  }
  if (field.status === "supplied" || field.status === "confirmed") {
    return value ? "filled" : "empty";
  }
  return "empty";
}

function emptyStats(): AlliancePhaseStats {
  return { total: 0, filled: 0, pending: 0, empty: 0 };
}

export function phaseStats(document: AllianceProfileDocument, phase: number): AlliancePhaseStats {
  const stats = emptyStats();
  for (const section of document.sections ?? []) {
    if (section.phase !== phase) continue;
    for (const field of section.fields ?? []) {
      stats.total += 1;
      stats[fieldBucket(field)] += 1;
    }
  }
  return stats;
}

export function documentStats(document: AllianceProfileDocument): AllianceDocumentStats {
  const byPhase: Record<number, AlliancePhaseStats> = { 1: emptyStats(), 2: emptyStats(), 3: emptyStats() };
  for (const phase of [1, 2, 3]) {
    byPhase[phase] = phaseStats(document, phase);
  }
  const total = emptyStats();
  for (const phase of [1, 2, 3]) {
    total.total += byPhase[phase].total;
    total.filled += byPhase[phase].filled;
    total.pending += byPhase[phase].pending;
    total.empty += byPhase[phase].empty;
  }
  return { ...total, byPhase };
}

// firstIncompletePhase returns the first phase that still has a field which is
// neither filled nor explicitly marked not-applicable. Phases without fields
// are skipped; when everything is complete it falls back to phase 1.
export function firstIncompletePhase(document: AllianceProfileDocument): number {
  for (const phase of [1, 2, 3]) {
    const stats = phaseStats(document, phase);
    if (stats.total > 0 && stats.filled < stats.total) return phase;
  }
  return 1;
}
