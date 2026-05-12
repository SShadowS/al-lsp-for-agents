// Stand-in for Base Application's Approvals Mgmt. codeunit.
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
