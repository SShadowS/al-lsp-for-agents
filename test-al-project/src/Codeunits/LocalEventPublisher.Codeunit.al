// Fixture for agent-flows/find_callers.py. Local codeunit that
// publishes a single [IntegrationEvent]. The companion fixture
// LocalEventSubscriber.Codeunit.al subscribes to OnMyLocalEvent — the
// scenario asks the agent to find that subscriber via the LSP's call
// hierarchy / incomingCalls path.
codeunit 50060 "Local Event Publisher"
{
    procedure DoSomethingWorthHandling()
    var
        Handled: Boolean;
    begin
        Handled := false;
        OnMyLocalEvent(Handled);
        if not Handled then
            Message('Default handling');
    end;

    [IntegrationEvent(false, false)]
    local procedure OnMyLocalEvent(var Handled: Boolean)
    begin
        // Publisher body is intentionally empty — that's the AL convention
        // for IntegrationEvent stubs. The interesting behavior is in
        // subscribers.
    end;
}
