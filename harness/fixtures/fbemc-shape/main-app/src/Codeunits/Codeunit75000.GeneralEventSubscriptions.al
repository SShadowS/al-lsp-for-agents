codeunit 75000 "General Event Subscriptions"
{
    procedure LogEvent(SourceNo: Code[20])
    var
        LogEntry: Record "FBEMC Log Entry";
    begin
        LogEntry.Init();
        LogEntry."Source No." := SourceNo;
        LogEntry."Created At" := CurrentDateTime();
        LogEntry.Insert(true);
    end;

    procedure CountEvents(): Integer
    var
        LogEntry: Record "FBEMC Log Entry";
    begin
        exit(LogEntry.Count());
    end;
}
