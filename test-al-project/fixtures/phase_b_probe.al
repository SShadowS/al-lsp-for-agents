codeunit 50199 "Phase B Probe"
{
    // Subscriber to a real Base Application event — exercises hover enrichment.
    [EventSubscriber(ObjectType::Codeunit, Codeunit::"Approvals Mgmt.", 'OnSendPurchaseDocForApproval', '', false, false)]
    local procedure OnSendPurchaseDocForApproval(var PurchaseHeader: Record "Purchase Header")
    begin
    end;

    // Local event publishers — exercise documentSymbol overlay tagging.
    [IntegrationEvent(false, false)]
    procedure OnAfterProbeRun(Result: Boolean)
    begin
    end;

    [BusinessEvent(false)]
    local procedure OnProbeBusiness(Amount: Decimal)
    begin
    end;

    // Regular procedure — should NOT be tagged as event.
    procedure RegularProc()
    begin
    end;
}
