import { documentStats, fieldBucket, firstIncompletePhase, phaseStats } from "./alliance-profile-stats";
import type { AllianceProfileDocument, AllianceProfileField, AllianceProfileSection } from "./types";
import assert from "node:assert/strict";
import test from "node:test";

function field(id: string, status: AllianceProfileField["status"], value: string): AllianceProfileField {
  return { id, title: id, value, status, source: "" };
}

function section(id: string, phase: number, fields: AllianceProfileField[]): AllianceProfileSection {
  return { id, title: id, phase, fields };
}

function document(sections: AllianceProfileSection[]): AllianceProfileDocument {
  return { schema_version: 1, sections };
}

test("fieldBucket only counts verified content", () => {
  assert.equal(fieldBucket(field("a", "supplied", "Java")), "filled");
  assert.equal(fieldBucket(field("b", "confirmed", "Java")), "filled");
  assert.equal(fieldBucket(field("c", "not_applicable", "自研，无 CMS")), "filled");
  assert.equal(fieldBucket(field("d", "not_applicable", "")), "empty");
  assert.equal(fieldBucket(field("e", "unknown", "")), "empty");
  assert.equal(fieldBucket(field("f", "unknown", "待确认：未授权")), "pending");
  assert.equal(fieldBucket(field("g", "supplied", "   ")), "empty");
});

test("phase and document stats separate pending notes from filled fields", () => {
  const doc = document([
    section("phase-1", 1, [
      field("S1-01", "supplied", "订单平台"),
      field("S1-02", "unknown", ""),
      field("S1-03", "unknown", "待确认：联系人未提供"),
      field("S1-04", "not_applicable", "无此功能"),
    ]),
    section("phase-2", 2, [field("S2-01", "confirmed", "Java 17")]),
  ]);
  assert.deepEqual(phaseStats(doc, 1), { total: 4, filled: 2, pending: 1, empty: 1 });
  const total = documentStats(doc);
  assert.equal(total.total, 5);
  assert.equal(total.filled, 3);
  assert.equal(total.pending, 1);
  assert.equal(total.empty, 1);
});

test("firstIncompletePhase skips fully filled phases and empty phases", () => {
  const doc = document([
    section("phase-1", 1, [field("S1-01", "supplied", "订单平台")]),
    section("phase-3", 3, [field("S3-10", "unknown", "")]),
  ]);
  assert.equal(firstIncompletePhase(doc), 3);
  assert.equal(firstIncompletePhase(document([section("phase-1", 1, [field("S1-01", "supplied", "x")])])), 1);
});
