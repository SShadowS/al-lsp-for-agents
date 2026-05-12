// Stand-in for Base Application's Permission Manager codeunit.
// Exercises a URI whose app segment is the workspace name, forcing
// the wrapper's content-scan fallback.
codeunit 9000 "Permission Manager"
{
    procedure HasUserCustomPermissions(UserSecID: Guid): Boolean
    begin
        exit(false);
    end;
}
