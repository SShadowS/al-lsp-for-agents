import { strict as assert } from "node:assert";
import { describe, it } from "mocha";
import { buildVsixUrl, splitExtensionId } from "../lib/marketplace.js";

describe("marketplace", () => {
  it("splits a publisher.name id", () => {
    assert.deepEqual(splitExtensionId("ms-dynamics-smb.al"), {
      publisher: "ms-dynamics-smb",
      name: "al",
    });
  });

  it("rejects an id without a dot", () => {
    assert.throws(() => splitExtensionId("notvalid"), /publisher\.name/);
  });

  it("rejects an id with multiple dots in publisher", () => {
    assert.deepEqual(splitExtensionId("foo.bar.baz"), {
      publisher: "foo",
      name: "bar.baz",
    });
  });

  it("builds the Marketplace vspackage URL", () => {
    const url = buildVsixUrl("ms-dynamics-smb.al", "18.0.2190758");
    assert.equal(
      url,
      "https://marketplace.visualstudio.com/_apis/public/gallery/publishers/ms-dynamics-smb/vsextensions/al/18.0.2190758/vspackage"
    );
  });
});
