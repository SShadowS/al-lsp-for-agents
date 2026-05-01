import { strict as assert } from "node:assert";
import { join } from "node:path";
import * as vscode from "vscode";

const FIXTURE = process.env.HARNESS_FIXTURE!;
const CELL = process.env.HARNESS_CELL!;

/**
 * Reference position in src/Tables/Customer.Table.al at which both:
 *   - the wrapper's bclsp_goToDefinition Language Model Tool returns a result
 *   - TODO: a builtin AL definition query at the same position should match
 *
 * Target: `CustomerMgt` on line 16 (1-based) of Customer.Table.al.
 * `CustomerMgt` is a Codeunit variable whose definition lives in
 * src/Codeunits/CustomerMgt.Codeunit.al, which is also in the fixture.
 * This is a stable cross-file reference that won't drift unless the
 * var block is moved.
 */
const REFERENCE = {
  file: "src/Tables/Customer.Table.al",
  line: 15,      // 0-based (line 16 in editor)
  character: 16, // 0-based — first char of `CustomerMgt`
};

describe(`parity: ${CELL}`, function () {
  this.timeout(120_000);

  // Only run parity tests on cells that include the local wrapper.
  before(async function () {
    if (
      CELL !== "cell-with-wrapper" &&
      CELL !== "cell-all-three" &&
      CELL !== "cell-isolated-cache"
    ) {
      this.skip();
      return;
    }
    // Ensure extensions are activated so their commands are registered.
    const alExt = vscode.extensions.getExtension("ms-dynamics-smb.al");
    if (alExt) await alExt.activate();
    const wrapperExt = vscode.extensions.getExtension(
      "SShadowSdk.al-lsp-for-agents"
    );
    if (wrapperExt) await wrapperExt.activate();
  });

  it("definition provider parity at reference position", async function () {
    // TODO(parity): The MS AL extension does NOT register a standard LSP
    // `textDocument/definition` provider — it uses a custom `al/gotodefinition`
    // LSP request instead. Therefore `vscode.executeDefinitionProvider` always
    // returns an empty array for AL files, making direct VS Code built-in
    // provider comparison impossible.
    //
    // The correct parity check compares:
    //   - wrapper bclsp_goToDefinition result (via alLspForAgents._test_invokeTool)
    //   - AL extension al/gotodefinition result (via the AL extension's LSP client)
    //
    // Implementing that comparison requires either:
    //   a) A second hidden test command in the AL extension itself (not feasible —
    //      we don't control that extension), or
    //   b) Both sides go through the wrapper's LSP client and we compare the raw
    //      al/gotodefinition response against bclsp_goToDefinition output (Layer 1
    //      refactor, tracked in future work).
    //
    // This test is skipped until Layer 1 is implemented and the wrapper can proxy
    // al/gotodefinition results for comparison.
    this.skip();

    // The following assertion would run post-Layer 1:
    const uri = vscode.Uri.file(join(FIXTURE, REFERENCE.file));
    await vscode.workspace.openTextDocument(uri);
    const position = new vscode.Position(REFERENCE.line, REFERENCE.character);

    // NOTE: executeDefinitionProvider always returns [] for AL files.
    // Replace with al/gotodefinition invocation once Layer 1 is in place.
    const builtin = (await vscode.commands.executeCommand(
      "vscode.executeDefinitionProvider",
      uri,
      position
    )) as vscode.Location[] | vscode.LocationLink[];

    const wrapperResult = await vscode.commands.executeCommand<{
      value: string;
    }>(
      "alLspForAgents._test_invokeTool",
      "bclsp_goToDefinition",
      {
        uri: uri.toString(),
        line: REFERENCE.line + 1,      // tool uses 1-based lines
        character: REFERENCE.character + 1,
      }
    );

    assert.ok(builtin && builtin.length > 0, "builtin returned no definition");
    assert.ok(wrapperResult, "wrapper returned no result");

    const wrapperLocations = JSON.parse(wrapperResult.value) as Array<{
      uri: string;
      range: { start: { line: number; character: number } };
    }>;
    assert.equal(
      wrapperLocations.length,
      builtin.length,
      `definition count differs: builtin=${builtin.length}, wrapper=${wrapperLocations.length}`
    );
  });
});
