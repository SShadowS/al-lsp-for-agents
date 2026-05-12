// Fixture for agent-flows/subscribe_to_event.py. The event-name string
// literal `'OnSendPurchaseDocForApproval'` on line 4 is the hover target
// — the wrapper's enrichEventReferenceHover should detect this is a
// publisher reference inside an [EventSubscriber(...)] attribute and
// append the publisher's full signature + attribute kind + source app
// to the hover response.
codeunit 50051 "Approvals Mgmt. Sub Demo"
{
    [EventSubscriber(ObjectType::Codeunit, Codeunit::"Approvals Mgmt.", 'OnSendPurchaseDocForApproval', '', false, false)]
    local procedure HandleOnSendPurchaseDocForApproval(var Sender: Variant; var IsHandled: Boolean)
    begin
        // Intentionally minimal. The agent's task is to read the hover
        // (via the wrapper's overlay) to learn the correct signature for
        // this subscriber, not to implement business logic.
    end;
}
