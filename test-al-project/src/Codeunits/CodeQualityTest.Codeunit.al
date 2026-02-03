codeunit 50099 "Code Quality Test"
{
    // This procedure has high cyclomatic complexity (should trigger critical warning)
    procedure HighComplexityProcedure(input: Integer): Integer
    var
        result: Integer;
    begin
        result := 0;

        if input > 0 then
            result := 1
        else
            result := -1;

        if input > 10 then
            if input > 20 then
                if input > 30 then
                    result := 30
                else
                    result := 20
            else
                result := 10;

        case input of
            1: result := 100;
            2: result := 200;
            3: result := 300;
            4: result := 400;
            else result := 0;
        end;

        if (input > 0) and (input < 100) then
            result := result + 1;

        if (input > 50) or (input < -50) then
            result := result * 2;

        exit(result);
    end;

    // This procedure has too many parameters (should trigger critical warning)
    procedure TooManyParameters(
        param1: Integer;
        param2: Text;
        param3: Boolean;
        param4: Decimal;
        param5: Date;
        param6: Time;
        param7: Code[20];
        param8: Guid
    )
    begin
        // Do something with all these parameters
    end;

    // This procedure has moderate parameters (should trigger info warning)
    procedure ModerateParameters(
        param1: Integer;
        param2: Text;
        param3: Boolean;
        param4: Decimal;
        param5: Date
    )
    begin
        // 5 parameters - above warning threshold
    end;

    // This procedure is never called (should trigger unused-procedure hint)
    procedure UnusedHelper()
    begin
        Message('I am never called');
    end;

    // This is a long method (should trigger long-method warning)
    procedure LongMethod()
    var
        i: Integer;
        j: Integer;
        k: Integer;
    begin
        // Line 1
        i := 1;
        // Line 2
        i := 2;
        // Line 3
        i := 3;
        // Line 4
        i := 4;
        // Line 5
        i := 5;
        // Line 6
        i := 6;
        // Line 7
        i := 7;
        // Line 8
        i := 8;
        // Line 9
        i := 9;
        // Line 10
        i := 10;
        // Line 11
        j := 1;
        // Line 12
        j := 2;
        // Line 13
        j := 3;
        // Line 14
        j := 4;
        // Line 15
        j := 5;
        // Line 16
        j := 6;
        // Line 17
        j := 7;
        // Line 18
        j := 8;
        // Line 19
        j := 9;
        // Line 20
        j := 10;
        // Line 21
        k := 1;
        // Line 22
        k := 2;
        // Line 23
        k := 3;
        // Line 24
        k := 4;
        // Line 25
        k := 5;
        // Line 26
        k := 6;
        // Line 27
        k := 7;
        // Line 28
        k := 8;
        // Line 29
        k := 9;
        // Line 30
        k := 10;
        // Line 31
        i := i + 1;
        // Line 32
        i := i + 2;
        // Line 33
        i := i + 3;
        // Line 34
        i := i + 4;
        // Line 35
        i := i + 5;
        // Line 36
        i := i + 6;
        // Line 37
        i := i + 7;
        // Line 38
        i := i + 8;
        // Line 39
        i := i + 9;
        // Line 40
        i := i + 10;
        // Line 41
        j := j + 1;
        // Line 42
        j := j + 2;
        // Line 43
        j := j + 3;
        // Line 44
        j := j + 4;
        // Line 45
        j := j + 5;
        // Line 46
        j := j + 6;
        // Line 47
        j := j + 7;
        // Line 48
        j := j + 8;
        // Line 49
        j := j + 9;
        // Line 50
        j := j + 10;
        // Line 51 - over 50 lines now
        k := k + i + j;
    end;

    // Simple procedure - should have no warnings
    procedure SimpleProcedure()
    begin
        Message('Hello');
    end;

    // Moderate complexity - should trigger info warning (complexity ~6)
    procedure ModerateComplexity(value: Integer): Boolean
    begin
        if value > 0 then
            if value > 10 then
                exit(true)
            else
                exit(false);

        if value < -10 then
            exit(true);

        exit(false);
    end;
}
