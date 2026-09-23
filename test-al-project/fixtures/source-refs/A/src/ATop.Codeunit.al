codeunit 50100 "A Top"
{
    procedure Top(): Text
    var
        Mid: Codeunit "B Mid";
        Lib: Codeunit "C Lib";
        Nope: Codeunit "Does Not Exist";
    begin
        Lib.Hello();
        exit(Mid.Mid());
    end;
}
