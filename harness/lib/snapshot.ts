import { writeFile, mkdir } from "node:fs/promises";
import { dirname, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import type { DiagSnapshot, NormalizedDiag } from "./types.js";

/**
 * Untyped diagnostic shape we accept from VS Code's API. We deliberately
 * don't import vscode types here so this module is testable in plain Node.
 * The fields match `vscode.Diagnostic` flattened with its uri.
 */
export interface RawDiag {
  uri: string; // file:// uri string
  source?: string;
  code?: string | number | { value: string | number; target?: unknown };
  severity: number; // 0=Error 1=Warning 2=Info 3=Hint per VS Code
  line: number; // 0-based
  character: number; // 0-based
}

const SEVERITY_NAMES = ["Error", "Warning", "Information", "Hint"] as const;

function severityName(n: number): string {
  return SEVERITY_NAMES[n] ?? `Unknown(${n})`;
}

function codeToString(
  code: RawDiag["code"]
): string {
  if (code === undefined || code === null) return "";
  if (typeof code === "object") {
    return String(code.value);
  }
  return String(code);
}

/**
 * Convert a file:// URI to a path that is relative to the fixture root,
 * using forward slashes. Handles percent-encoding (e.g. `c%3A` for `C:`).
 */
export function relativizeUri(uri: string, fixtureRoot: string): string {
  let p = uri;
  if (p.startsWith("file:///")) p = p.slice("file:///".length);
  else if (p.startsWith("file://")) p = p.slice("file://".length);
  p = decodeURIComponent(p);
  // On Windows the result is "C:/work/..." after stripping. On Linux it's
  // "work/..." (we lost the leading slash). Add it back when needed.
  if (!/^[a-zA-Z]:[\\/]/.test(p) && !p.startsWith("/")) {
    p = "/" + p;
  }
  // path.relative needs same-style separators on Windows.
  let rel = relative(fixtureRoot, p);
  if (sep === "\\") {
    rel = rel.replace(/\\/g, "/");
  }
  return rel;
}

export function normalizeDiagnostics(
  raw: RawDiag[],
  fixtureRoot: string
): NormalizedDiag[] {
  const out: NormalizedDiag[] = raw.map((d) => ({
    relUri: relativizeUri(d.uri, fixtureRoot),
    source: d.source ?? "",
    code: codeToString(d.code),
    severity: severityName(d.severity),
    line: d.line,
    character: d.character,
  }));

  out.sort((a, b) => {
    if (a.relUri !== b.relUri) return a.relUri < b.relUri ? -1 : 1;
    if (a.line !== b.line) return a.line - b.line;
    if (a.character !== b.character) return a.character - b.character;
    if (a.code !== b.code) return a.code < b.code ? -1 : 1;
    if (a.source !== b.source) return a.source < b.source ? -1 : 1;
    return 0;
  });
  return out;
}

export function diagnosticsEqual(
  a: NormalizedDiag[],
  b: NormalizedDiag[]
): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const x = a[i]!;
    const y = b[i]!;
    if (
      x.relUri !== y.relUri ||
      x.source !== y.source ||
      x.code !== y.code ||
      x.severity !== y.severity ||
      x.line !== y.line ||
      x.character !== y.character
    ) {
      return false;
    }
  }
  return true;
}

export async function writeSnapshot(
  path: string,
  snapshot: DiagSnapshot
): Promise<void> {
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, JSON.stringify(snapshot, null, 2) + "\n");
}
