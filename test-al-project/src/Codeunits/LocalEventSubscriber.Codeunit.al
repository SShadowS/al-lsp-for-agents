// Fixture for agent-flows/find_callers.py. Subscribes to
// LocalEventPublisher's OnMyLocalEvent. The scenario expects the agent
// to discover THIS procedure via call hierarchy from the publisher.
codeunit 50061 "Local Event Subscriber"
{
    [EventSubscriber(ObjectType::Codeunit, Codeunit::"Local Event Publisher", 'OnMyLocalEvent', '', false, false)]
    local procedure HandleMyLocalEvent(var Handled: Boolean)
    begin
        Handled := true;
    end;
}
