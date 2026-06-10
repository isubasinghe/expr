package vm_test

import (
	"os"
	"strings"
	"testing"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/internal/testify/require"
	"github.com/expr-lang/expr/vm"
)

// TestAnalyzeCFG_corpus checks structural invariants of the CFG over every
// expression in the generated corpus that compiles without an environment.
func TestAnalyzeCFG_corpus(t *testing.T) {
	b, err := os.ReadFile("../testdata/generated.txt")
	require.NoError(t, err)

	analyzed, loops := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		program, err := expr.Compile(line)
		if err != nil {
			continue
		}
		cfg := vm.AnalyzeCFG(program)
		analyzed++
		loops += len(cfg.Loops)

		// Blocks partition the bytecode in order.
		prev := 0
		for _, block := range cfg.Blocks {
			require.Equal(t, prev, block.Start, line)
			require.Greater(t, block.End, block.Start, line)
			prev = block.End
			for _, s := range block.Succs {
				require.GreaterOrEqual(t, s, 0, line)
				require.Less(t, s, len(cfg.Blocks), line)
			}
		}
		require.Equal(t, len(program.Bytecode), prev, line)

		for l, loop := range cfg.Loops {
			require.Equal(t, vm.OpJumpBackward, program.Bytecode[loop.Backedge], line)
			require.LessOrEqual(t, loop.Head, loop.Backedge, line)
			require.Equal(t, loop.Head, loop.Backedge+1-program.Arguments[loop.Backedge], line)

			// Loop intervals are disjoint or strictly nested.
			for m, other := range cfg.Loops {
				if m == l {
					continue
				}
				disjoint := other.Backedge < loop.Head || loop.Backedge < other.Head
				lInM := other.Head <= loop.Head && loop.Backedge <= other.Backedge
				mInL := loop.Head <= other.Head && other.Backedge <= loop.Backedge
				require.True(t, disjoint || lInM || mInL, line)
			}

			// Parent/children agree and parent encloses child.
			if loop.Parent != -1 {
				parent := cfg.Loops[loop.Parent]
				require.LessOrEqual(t, parent.Head, loop.Head, line)
				require.LessOrEqual(t, loop.Backedge, parent.Backedge, line)
				require.Contains(t, parent.Children, l, line)
			}

			// One iteration costs at least the head and backedge blocks
			// and at most the whole loop interval.
			cost := cfg.PerIterationCost(l)
			require.Greater(t, cost, 0, line)
			require.LessOrEqual(t, cost, loop.Backedge-loop.Head+1, line)
		}
	}

	// Guard against the corpus silently going vacuous.
	require.Greater(t, analyzed, 1000)
	require.Greater(t, loops, 100)
}

func TestAnalyzeCFG_conditional(t *testing.T) {
	program, err := expr.Compile(`a ? 1 : 2`)
	require.NoError(t, err)

	cfg := vm.AnalyzeCFG(program)

	// Compiled shape: <cond> OpJumpIfFalse; OpPop <then> OpJump; OpPop <else>
	require.Len(t, cfg.Blocks, 3)

	// Blocks partition the bytecode in order.
	require.Equal(t, 0, cfg.Blocks[0].Start)
	for i := 1; i < len(cfg.Blocks); i++ {
		require.Equal(t, cfg.Blocks[i-1].End, cfg.Blocks[i].Start)
	}
	require.Equal(t, len(program.Bytecode), cfg.Blocks[2].End)

	// The condition block branches to both arms.
	require.Equal(t, vm.OpJumpIfFalse, program.Bytecode[cfg.Blocks[0].End-1])
	require.Equal(t, []int{1, 2}, cfg.Blocks[0].Succs)

	// The then-arm jumps past the else-arm, off the end of the program.
	require.Equal(t, vm.OpJump, program.Bytecode[cfg.Blocks[1].End-1])
	require.Empty(t, cfg.Blocks[1].Succs)

	// The else-arm falls off the end of the program.
	require.Empty(t, cfg.Blocks[2].Succs)
}

func TestAnalyzeCFG_loop(t *testing.T) {
	program, err := expr.Compile(`filter(1..3, # > 0)`)
	require.NoError(t, err)

	cfg := vm.AnalyzeCFG(program)

	require.Len(t, cfg.Loops, 1)
	loop := cfg.Loops[0]

	require.Equal(t, vm.OpJumpBackward, program.Bytecode[loop.Backedge])
	require.Less(t, loop.Head, loop.Backedge)
	// The backedge jumps to the head: target = ip+1-arg.
	require.Equal(t, loop.Head, loop.Backedge+1-program.Arguments[loop.Backedge])

	// The loop head is a block leader.
	heads := 0
	for _, b := range cfg.Blocks {
		if b.Start == loop.Head {
			heads++
		}
	}
	require.Equal(t, 1, heads)
}

func TestAnalyzeCFG_nestedLoops(t *testing.T) {
	program, err := expr.Compile(`all(1..3, any(1..2, # > 0))`)
	require.NoError(t, err)

	cfg := vm.AnalyzeCFG(program)

	require.Len(t, cfg.Loops, 2)

	// Identify the outer loop as the one whose interval contains the other.
	outer, inner := 0, 1
	if cfg.Loops[1].Head < cfg.Loops[0].Head {
		outer, inner = 1, 0
	}
	require.Less(t, cfg.Loops[outer].Head, cfg.Loops[inner].Head)
	require.Less(t, cfg.Loops[inner].Backedge, cfg.Loops[outer].Backedge)

	require.Equal(t, -1, cfg.Loops[outer].Parent)
	require.Equal(t, []int{inner}, cfg.Loops[outer].Children)
	require.Equal(t, outer, cfg.Loops[inner].Parent)
	require.Empty(t, cfg.Loops[inner].Children)
}

func TestAnalyzeCFG_siblingLoops(t *testing.T) {
	program, err := expr.Compile(`all(1..3, # > 0) and any(1..2, # > 1)`)
	require.NoError(t, err)

	cfg := vm.AnalyzeCFG(program)

	require.Len(t, cfg.Loops, 2)
	for _, loop := range cfg.Loops {
		require.Equal(t, -1, loop.Parent)
		require.Empty(t, loop.Children)
	}
}

func TestCFG_PerIterationCost(t *testing.T) {
	// A hand-built loop with a conditional inside the body, so the two
	// paths through one iteration have different lengths:
	//
	//   0: OpInt                          preamble
	//   1: OpJumpIfEnd   +4 -> 6 (exit)   loop head
	//   2: OpJumpIfFalse +1 -> 4          branch: skip the then-arm
	//   3: OpInt                          then-arm
	//   4: OpIncrementIndex
	//   5: OpJumpBackward 5 -> 1          backedge
	//
	// Worst-case iteration takes the fall-through at 2: instructions
	// 1,2,3,4,5 = cost 5. The short path (1,2,4,5) costs 4.
	program := &vm.Program{
		Bytecode: []vm.Opcode{
			vm.OpInt,
			vm.OpJumpIfEnd,
			vm.OpJumpIfFalse,
			vm.OpInt,
			vm.OpIncrementIndex,
			vm.OpJumpBackward,
		},
		Arguments: []int{0, 4, 1, 0, 0, 5},
	}

	cfg := vm.AnalyzeCFG(program)

	require.Len(t, cfg.Loops, 1)
	require.Equal(t, 1, cfg.Loops[0].Head)
	require.Equal(t, 5, cfg.Loops[0].Backedge)
	require.Equal(t, 5, cfg.PerIterationCost(0))
}

func TestCFG_PerIterationCost_nested(t *testing.T) {
	// An outer iteration must not be charged for inner loop iterations:
	// the worst path through the outer body passes the inner loop via its
	// zero-iteration exit. Inner iterations are charged at the inner
	// backedge instead.
	//
	//   0: OpJumpIfEnd   +5 -> 6 (exit)   outer head
	//   1: OpJumpIfEnd   +2 -> 4          inner head
	//   2: OpInt                          inner body
	//   3: OpJumpBackward 3 -> 1          inner backedge
	//   4: OpIncrementIndex
	//   5: OpJumpBackward 6 -> 0          outer backedge
	program := &vm.Program{
		Bytecode: []vm.Opcode{
			vm.OpJumpIfEnd,
			vm.OpJumpIfEnd,
			vm.OpInt,
			vm.OpJumpBackward,
			vm.OpIncrementIndex,
			vm.OpJumpBackward,
		},
		Arguments: []int{5, 2, 0, 3, 0, 6},
	}

	cfg := vm.AnalyzeCFG(program)

	require.Len(t, cfg.Loops, 2)
	outer, inner := 0, 1
	if cfg.Loops[1].Head < cfg.Loops[0].Head {
		outer, inner = 1, 0
	}
	// Inner: instructions 1,2,3.
	require.Equal(t, 3, cfg.PerIterationCost(inner))
	// Outer: instructions 0,1,4,5 — the inner body (2,3) is not charged
	// because the inner backedge is a dead end on the forward DAG.
	require.Equal(t, 4, cfg.PerIterationCost(outer))
}

func TestAnalyzeCFG_straightLine(t *testing.T) {
	program, err := expr.Compile(`a + b`)
	require.NoError(t, err)

	cfg := vm.AnalyzeCFG(program)

	require.Len(t, cfg.Blocks, 1)
	require.Equal(t, 0, cfg.Blocks[0].Start)
	require.Equal(t, len(program.Bytecode), cfg.Blocks[0].End)
	require.Empty(t, cfg.Blocks[0].Succs)
	require.Empty(t, cfg.Loops)
}
