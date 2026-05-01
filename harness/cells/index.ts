import { cellControl } from "./cell-control.js";
import { cellWithWrapper } from "./cell-with-wrapper.js";
import { cellWithTestRunner } from "./cell-with-test-runner.js";
import { cellAllThree } from "./cell-all-three.js";
import { cellIsolatedCache } from "./cell-isolated-cache.js";
import type { CellConfig } from "../lib/types.js";

export const ALL_CELLS: CellConfig[] = [
  cellControl,
  cellWithWrapper,
  cellWithTestRunner,
  cellAllThree,
  cellIsolatedCache,
];

export function findCell(name: string): CellConfig {
  const cell = ALL_CELLS.find((c) => c.name === name);
  if (!cell) {
    throw new Error(
      `Unknown cell "${name}". Known: ${ALL_CELLS.map((c) => c.name).join(", ")}`
    );
  }
  return cell;
}
