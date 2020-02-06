# Block Witness Formal Specification

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED",  "MAY", and "OPTIONAL" in this document are to be interpreted as described in [RFC 2119](https://tools.ietf.org/html/rfc2119).

## The Stack

Witnesses are executed on a stack machine that builds tries/calculates hashes.

The stack consists of pairs `(node, hash)`.

Each witness is a queue of instructions.

In every execution cycle a single instruction gets dequeued and a matching substitution rule gets applied to the stack.

In the end, when there are no more instructions left, there MUST be only one item left on the stack.  

## Substitution rules

Here is an example substitution rule. The current instruction is named `INSTR1`.

```
STACK(node1, hash1) STACK(node2, hash2) INSTR1(params) |=> 
STACK(helper1(node1, node2), helper2(hash1, hash2))
```

This substitution rule replaces 2 stack items `(node1, hash1)` and `(node2, hash2)`
with a single stack item `(helper1(node1, node2), helper2(hash1, hash2))`.

Where `helper1` and `helper2` are pure functions defined in pseudocode (see the example below).

```
helper1 (value1, value2) {
    return value1 + value2
}

helper2 (hash1, hash2) {
    return KECCAK(hash1 + hash2)
}
```

The specification for a substitution rule

```
[GUARD <CONDITION> ...]

[ STACK(<node-var-name>, <hash-var-name>), ... ] <INSTRUCTION>[(<params>)] |=>
STACK(node-value, hash-value), ...
```

The substitution rule MAY have one or more GUARD statements.
The substitution rule MAY have one or more STACK statements before the instruction.
The substitution rule MUST have exactly one instruction.
The substitution rule MAY have parameters for the instruction.
The substitution rule MUST have at least one STACK statement after the arrow.

So, the minimal substitution rule is for the `HASH` instruction that pushes one hash to the stack:
```
HASH(hashValue) |=> STACK(hashNode(hashValue), hashValue)
```

## GUARDs

Each substitution rule can have zero, one or multiple `GUARD` statements.
Each `GUARD` statement looks like this:

```
GUARD <CONDITION>
```

That means that for the substitution rule to be applicable the `<CONDITION>` in the guard statement must be true.

If a substitution rule has multiple guard statements, all of the conditions specified there should be satisfied.

## `TRAP` statements

If no substitution rules are applicable for an instruction, then the execution MUST stop (`TRAP` instruction) and the partial results MUST NOT be used.

Implementation of the `TRAP` instruction is undefined, but it should stop the execution flow. (e.g. in Golang it might be a function returning an error or a panic).

## Instructions & Parameters

Each instruction MAY have one or more parameters.
The parameters values MUST be situated in the witness.
The parameter values MUST NOT be taken from the stack.

That makes it different from the helper function parameters that MAY come from the stack or MAY come from the witness.

## Helper functions

Helper functions are functions that are used in GUARDs or substitution rules.

Helper functions MUST be pure.
Helper functions MUST have at least one argument.
Helper functions MAY have variadic parameters: `HELPER_EXAMPLE(arg1, arg2, list...)`.
Helper functions MAY contain recursion.

## Data types

INTEGER - we treat integers as infinite, the overflow behaviour or mapping to the actual data types is undefined in this spec and should be dependent on implementation.

## Execution flow 

Let's look at the example.

Our example witness would be `HASH h1; HASH h2; BRANCH 0b11`.

Initial state of the stack is ` <empty> `;

---

**1. Executing `HASH h1`**: push a `hashNode` to the stack.

Witness: `HASH h2; BRANCH 0b11`

Stack: `(hashNode(h1), h1)`

---

**2. Executing `HASH h2`**: push one more `hashNode` to the stack.

Witness `BRANCH 0b11`

Stack: `(hashNode(h2), h2); (hashNode(h1), h1)`

---

**3. Executing `BRANCH 0b11`**: replace 2 items on the stack with a single `fullNode`.

Witness: ` <empty> `

Stack: `(fullNode{0: hashNode(h2), 1: hashNode(h1)}, KECCAK(h2+h1))`

---

So our stack has exactly a single item and there are no more instructions in the witness, the execution is completed.

## Modes

There are two modes of execution for this stack machine:

(1) **normal execution** -- the mode that constructs a trie;

(2) **hash only execution** -- the mode that calculates the root hashe of a trie without constructing the tries itself;

In the mode (2), the first part of the pair `(node, hash)` MUST NOT be used: `(nil, hash)`.

## Instructions

### `BRANCH mask`

This instruction pops `NBITSET(mask)` items from both node stack and hash stack (up to 16 for each one). Then it pushes a new branch node on the node stack that has children according to the stack; it also pushes a new hash to the hash stack.

**Substitution rules**
```

GUARD NBITSET(mask) == 2 

STACK(n0, h0) STACK(n1, h1) BRANCH(mask) |=> 
STACK(branchNode(MAKE_VALUES_ARRAY(mask, n0, n1)), keccak(CONCAT(MAKE_VALUES_ARRAY(mask, h0, n1))))
---


GUARD NBITSET(mask) == 3

STACK(n0, h0) STACK(n1, h1) STACK(n2, h2) BRANCH(mask) |=> 
STACK(branchNode(MAKE_VALUES_ARRAY(mask, n0, n1, n2)), keccak(CONCAT(MAKE_VALUES_ARRAY(mask, h0, n1, n2))))

---

...

---

GUARD NBITSET(mask) == 16

STACK(n0, h0) STACK(n1, h1) ... STACK(n15, h15) BRANCH(mask) |=>
STACK(branchNode(MAKE_VALUES_ARRAY(mask, n0, n1, ..., n15)), keccak(CONCAT(MAKE_VALUES_ARRAY(mask, h0, n1, ..., n15))))
```

## Helper functions

### `MAKE_VALUES_ARRAY`

returns an array of 16 elements, where values from `values` are set to the indices where `mask` has bits set to 1. Every other place has `nil` value there.

**Example**: `MAKE_VALUES_ARRAY(5, [a, b])` returns `[a, nil, b, nil, nil, ..., nil]` (binary representation of 5 is `0000000000000101`)

```
MAKE_VALUES_ARRAY(mask, values...) {
    return MAKE_VALUES_ARRAY(mask, 0, values)
}

MAKE_VALUES_ARRAY(mask, idx, values...) {
    if idx > 16 {
        return []
    }

    if BIT_TEST(mask, idx) {
        return PREPEND(FIRST(values), (MAKE_VALUES_ARRAY mask, INC(idx), REST(values)))
    } else {
        return PREPEND(nil, (MAKE_VALUES_ARRAY mask, INC(idx), values))
    }
}
```

### `NBITSET(number)`

returns number of bits set in the binary representation of `number`.

### `BIT_TEST(number, n)`

`n` MUST NOT be negative.

returns `true` if bit `n` in `number` is set, `false` otherwise.

### `PREPEND(value, array)`

returns a new array with the `value` at index 0 and `array` values starting from index 1

### `INC(value)`

increments `value` by 1