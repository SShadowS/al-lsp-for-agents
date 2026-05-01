codeunit 75200 "Use FBEMC"
{
    procedure Execute()
    var
        Subs: Codeunit "General Event Subscriptions";
    begin
        Subs.DoA();
    end;
}
