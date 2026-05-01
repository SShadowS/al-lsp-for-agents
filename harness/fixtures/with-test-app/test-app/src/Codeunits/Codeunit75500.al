codeunit 75500 "FBEMC Tests"
{
    Subtype = Test;
    TestPermissions = Disabled;

    [Test]
    procedure TestMainProcedure()
    var
        Subs: Codeunit "General Event Subscriptions";
    begin
        if Subs.DoMain() <> 42 then
            Error('expected 42');
    end;
}
