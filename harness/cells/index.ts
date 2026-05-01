import { cellControl } from "./cell-control.js";
import { cellWithWrapper } from "./cell-with-wrapper.js";
import { cellWithTestRunner } from "./cell-with-test-runner.js";
import { cellAllThree } from "./cell-all-three.js";
import { cellIsolatedCache } from "./cell-isolated-cache.js";
import { cellAllThreeFbemc } from "./cell-all-three-fbemc.js";
import { cellAllThreePkgCollision } from "./cell-all-three-pkg-collision.js";
import { cellAllThreeMulti } from "./cell-all-three-multi.js";
import { cellAllThreeTestApp } from "./cell-all-three-test-app.js";
import { cellRealDoSupport } from "./cell-real-do-support.js";
import { cellRealNoWrapper } from "./cell-real-no-wrapper.js";
import { cellRealNoTr } from "./cell-real-no-tr.js";
import { cellRealControl } from "./cell-real-control.js";
import { cellRealIsolatedCache } from "./cell-real-isolated-cache.js";
import { cellBisect } from "./cell-bisect.js";
import type { CellConfig } from "../lib/types.js";

export const ALL_CELLS: CellConfig[] = [
  cellControl,
  cellWithWrapper,
  cellWithTestRunner,
  cellAllThree,
  cellIsolatedCache,
  cellAllThreeFbemc,
  cellAllThreePkgCollision,
  cellAllThreeMulti,
  cellAllThreeTestApp,
  cellRealDoSupport,
  cellRealNoWrapper,
  cellRealNoTr,
  cellRealControl,
  cellRealIsolatedCache,
  cellBisect,
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
