codeunit 75200 "Use FBEMC"
{
    procedure Execute()
    var
        Subs: Codeunit "General Event Subscriptions";
    begin
        Subs.LogEvent('TEST');
    end;

    procedure CountAll(): Integer
    var
        Subs: Codeunit "General Event Subscriptions";
    begin
        exit(Subs.CountEvents());
    end;
}
