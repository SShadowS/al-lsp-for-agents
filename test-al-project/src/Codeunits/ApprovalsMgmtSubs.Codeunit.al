// Fixture for agent-flows scenarios. Provides a navigable Approvals Mgmt.
// reference so the agent can `goToDefinition` on it and have the wrapper
// resolve to a materialized .al cache file from Base Application's
// Codeunit 1535.
codeunit 50050 "Approvals Mgmt. Subs"
{
    // The agent's job: list [IntegrationEvent] publishers in Codeunit 1535
    // "Approvals Mgmt." (Base Application) that this codeunit could
    // subscribe to. The variable declaration below gives a starting point
    // for `goToDefinition` — char ~22 on line 12 lands on the codeunit
    // name "Approvals Mgmt." which the AL LSP resolves to Codeunit 1535.
    procedure NavigationAnchor()
    var
        ApprovalsMgmt: Codeunit "Approvals Mgmt.";
    begin
        // Reference the variable so it isn't dead-code-eliminated and so
        // `findReferences` returns this location too.
        if false then
            ApprovalsMgmt.OnSendDocForApproval();
    end;
}
