codeunit 50200 "B Mid"
{
    procedure Mid(): Text
    var
        Lib: Codeunit "C Lib";
    begin
        exit(Lib.Hello());
    end;
}
