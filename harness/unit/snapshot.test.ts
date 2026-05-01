import { strict as assert } from "node:assert";
import { describe, it } from "mocha";
import {
  normalizeDiagnostics,
  diagnosticsEqual,
  type RawDiag,
} from "../lib/snapshot.js";

describe("snapshot", () => {
  const fixtureRoot = "/work/fixture";

  it("normalizes a basic diagnostic", () => {
    const raw: RawDiag[] = [
      {
        uri: "file:///work/fixture/src/Customer.al",
        source: "al",
        code: "AL0118",
        severity: 0, // VS Code Error = 0
        line: 41,
        character: 12,
      },
    ];
    const out = normalizeDiagnostics(raw, fixtureRoot);
    assert.deepEqual(out, [
      {
        relUri: "src/Customer.al",
        source: "al",
        code: "AL0118",
        severity: "Error",
        line: 41,
        character: 12,
      },
    ]);
  });

  it("sorts deterministically by (relUri, line, character, code)", () => {
    const raw: RawDiag[] = [
      {
        uri: "file:///work/fixture/b.al",
        source: "al",
        code: "AL2",
        severity: 1,
        line: 5,
        character: 0,
      },
      {
        uri: "file:///work/fixture/a.al",
        source: "al",
        code: "AL1",
        severity: 1,
        line: 0,
        character: 0,
      },
      {
        uri: "file:///work/fixture/a.al",
        source: "al",
        code: "AL2",
        severity: 1,
        line: 0,
        character: 0,
      },
    ];
    const out = normalizeDiagnostics(raw, fixtureRoot);
    assert.deepEqual(out.map((d) => `${d.relUri}:${d.code}`), [
      "a.al:AL1",
      "a.al:AL2",
      "b.al:AL2",
    ]);
  });

  it("handles object-form code field", () => {
    const raw: RawDiag[] = [
      {
        uri: "file:///work/fixture/x.al",
        source: "al",
        code: { value: 42, target: "https://example" },
        severity: 0,
        line: 0,
        character: 0,
      },
    ];
    const out = normalizeDiagnostics(raw, fixtureRoot);
    assert.equal(out[0]?.code, "42");
  });

  it("handles missing code as empty string", () => {
    const raw: RawDiag[] = [
      {
        uri: "file:///work/fixture/x.al",
        source: "al",
        severity: 0,
        line: 0,
        character: 0,
      },
    ];
    const out = normalizeDiagnostics(raw, fixtureRoot);
    assert.equal(out[0]?.code, "");
  });

  it("normalizes Windows paths to forward slashes", () => {
    const raw: RawDiag[] = [
      {
        uri: "file:///c%3A/work/fixture/src/Customer.al",
        source: "al",
        code: "AL0118",
        severity: 0,
        line: 0,
        character: 0,
      },
    ];
    const out = normalizeDiagnostics(raw, "C:\\work\\fixture");
    assert.equal(out[0]?.relUri, "src/Customer.al");
  });

  it("diagnosticsEqual is field-for-field", () => {
    const a = [
      {
        relUri: "x.al",
        source: "al",
        code: "AL1",
        severity: "Error" as const,
        line: 0,
        character: 0,
      },
    ];
    const b = [
      {
        relUri: "x.al",
        source: "al",
        code: "AL1",
        severity: "Error" as const,
        line: 0,
        character: 0,
      },
    ];
    assert.equal(diagnosticsEqual(a, b), true);
    b[0]!.line = 1;
    assert.equal(diagnosticsEqual(a, b), false);
  });
});
