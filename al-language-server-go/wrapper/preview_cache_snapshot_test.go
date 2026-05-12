package wrapper

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// updateSnapshots regenerates golden files instead of asserting. Pass
// -update to refresh after intentional changes (e.g. extraction tweak).
//
//	go test ./wrapper/ -run TestPreviewCache_Materialize_Snapshots -update
var updateSnapshots = flag.Bool("update", false,
	"regenerate snapshot files under wrapper/testdata/snapshots/")

// snapshotCase describes a single materialization scenario: an
// al-preview URI, a curated .app archive built in a TempDir, and the
// snapshot file the materialized output is compared against.
//
// The fakeFiles map mimics MS's real layout: src/<Subdir>/<Name>.<Type>.al
// rather than flat src/. Tests should include enough variety in the map
// to exercise the basename-match + canonical-key fuzzy fallback paths.
type snapshotCase struct {
	name        string             // test subtest name + snapshot filename
	uri         string             // al-preview URI to materialize
	appFilename string             // archive filename under .alpackages
	fakeFiles   map[string]string  // path → content inside the .app zip
}

func TestPreviewCache_Materialize_Snapshots(t *testing.T) {
	cases := []snapshotCase{
		{
			// Object name with trailing dot. MS's real Approvals Mgmt.
			// (Codeunit 1535) lives at src/OtherCapabilities/Approvals/
			// ApprovalsMgmt.Codeunit.al — note: no space, single dot.
			// The fuzzy fallback (canonical-alnum) must match this.
			name: "codeunit_with_trailing_dot_in_name",
			uri: "al-preview:/allang/Base%20Application/Codeunit/1535/" +
				"Approvals%20Mgmt..dal",
			appFilename: "Microsoft_Base Application_26.1.33404.34053.app",
			fakeFiles: map[string]string{
				"NavxManifest.xml": "<App />",
				"src/OtherCapabilities/Approvals/ApprovalsMgmt.Codeunit.al":
					codeunitWithEvents,
			},
		},
		{
			// Object name with spaces, no special punctuation.
			// MS-style filename strips spaces; the canonical matcher
			// should bridge "Sales-Post" → "salespost".
			name: "codeunit_with_spaces_and_hyphen",
			uri: "al-preview:/allang/Base%20Application/Codeunit/80/" +
				"Sales-Post.dal",
			appFilename: "Microsoft_Base Application_26.1.33404.34053.app",
			fakeFiles: map[string]string{
				"NavxManifest.xml":              "<App />",
				"src/Sales/SalesPost.Codeunit.al": salesPostStub,
			},
		},
		{
			// URI app segment is the workspace name, not the source app.
			// Triggers the content-scan fallback because no .app
			// filename matches "test-al-project". Snapshot proves the
			// fallback finds the right archive AND the right file.
			name: "uri_uses_workspace_name_as_app_segment",
			uri: "al-preview:/allang/test-al-project/Codeunit/9000/" +
				"Permission%20Manager.dal",
			appFilename: "Microsoft_Base Application_26.1.33404.34053.app",
			fakeFiles: map[string]string{
				"NavxManifest.xml":             "<App />",
				"src/System/PermissionManager.Codeunit.al": permissionManagerStub,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			runSnapshotCase(t, c)
		})
	}
}

func runSnapshotCase(t *testing.T, c snapshotCase) {
	t.Helper()

	workspace := t.TempDir()
	pkgCache := filepath.Join(workspace, ".alpackages")
	if err := os.MkdirAll(pkgCache, 0o755); err != nil {
		t.Fatal(err)
	}
	appPath := filepath.Join(pkgCache, c.appFilename)
	buildFakeApp(t, appPath, c.fakeFiles)

	cache := newPreviewCache(workspace, t.TempDir())
	mapped, err := cache.materialize(c.uri, []string{pkgCache})
	if err != nil {
		t.Fatalf("materialize(%q): %v", c.uri, err)
	}

	cachePath, err := FileURIToPath(mapped)
	if err != nil {
		t.Fatalf("FileURIToPath(%q): %v", mapped, err)
	}
	got, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache file %q: %v", cachePath, err)
	}

	snapPath := filepath.Join("testdata", "snapshots", c.name+".al")
	if *updateSnapshots {
		if err := os.MkdirAll(filepath.Dir(snapPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(snapPath, got, 0o644); err != nil {
			t.Fatalf("write snapshot %q: %v", snapPath, err)
		}
		t.Logf("updated snapshot: %s (%d bytes)", snapPath, len(got))
		return
	}

	want, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf(
			"missing snapshot %q (run `go test ./wrapper/ -run "+
				"TestPreviewCache_Materialize_Snapshots -update` "+
				"to create): %v",
			snapPath, err,
		)
	}
	if string(got) != string(want) {
		t.Errorf(
			"snapshot mismatch for %s\n"+
				"snapshot: %s\n"+
				"got %d bytes, want %d bytes\n"+
				"got first 200: %q\n"+
				"want first 200: %q\n"+
				"(rerun with -update if the change is intentional)",
			c.name, snapPath, len(got), len(want),
			firstN(string(got), 200), firstN(string(want), 200),
		)
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// --- Curated source bodies used as snapshot inputs --------------------------
//
// These stand in for MS Base Application sources. Realistic enough to
// exercise the wrapper's path handling; small enough to read at a
// glance when a snapshot diff surfaces.

const codeunitWithEvents = `// Stand-in for Base Application's Approvals Mgmt. codeunit.
// Real upstream has 258 methods + 137 IntegrationEvents; we ship a
// curated subset that exercises the same shape.
codeunit 1535 "Approvals Mgmt."
{
    procedure SendApprovalRequest(): Boolean
    begin
        exit(true);
    end;

    [IntegrationEvent(false, false)]
    local procedure OnSendPurchaseDocForApproval(var PurchaseHeader: Record "Purchase Header")
    begin
    end;

    [IntegrationEvent(false, false)]
    local procedure OnCancelPurchaseApprovalRequest(var PurchaseHeader: Record "Purchase Header"; var IsHandled: Boolean)
    begin
    end;
}
`

const salesPostStub = `// Stand-in for Base Application's Sales-Post codeunit.
// Real one is one of BC's largest (~10k lines). We ship a shape stub.
codeunit 80 "Sales-Post"
{
    TableNo = "Sales Header";

    trigger OnRun()
    begin
    end;

    procedure Run(var SalesHdr: Record "Sales Header")
    begin
    end;
}
`

const permissionManagerStub = `// Stand-in for Base Application's Permission Manager codeunit.
// Exercises a URI whose app segment is the workspace name, forcing
// the wrapper's content-scan fallback.
codeunit 9000 "Permission Manager"
{
    procedure HasUserCustomPermissions(UserSecID: Guid): Boolean
    begin
        exit(false);
    end;
}
`
