// Stand-in for Base Application's Sales-Post codeunit.
// Real one is one of BC's largest (~10k lines). We ship a shape stub.
codeunit 80 "Sales-Post"
{
    TableNo = "Sales Header";

    trigger OnRun()
    begin
    end;

    procedure Run(var SalesHdr: Record "Sales Header")
    begin
    end;
}
