pragma solidity >=0.5.0;
contract selfDestructor {
    int value;

    constructor() public {
        value = 1;
    }

    function selfDestruct() public {
        address payable nil = address(0);
        selfdestruct(nil);
    }
}