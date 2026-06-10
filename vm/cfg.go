package vm

import "sort"

// BasicBlock is a maximal straight-line sequence of instructions
// [Start, End) with no jumps in or out except at the boundaries.
type BasicBlock struct {
	Start int   // index of the first instruction in the block
	End   int   // index one past the last instruction in the block
	Succs []int // indices into CFG.Blocks of successor blocks
}

// CFG is the control flow graph of a compiled program.
type CFG struct {
	Blocks []BasicBlock
	Loops  []Loop
}

// Loop is a natural loop discovered from an OpJumpBackward instruction.
// The compiler only emits structured loops, so the loop body is the
// contiguous instruction interval [Head, Backedge] and loops nest as
// strictly nested intervals.
type Loop struct {
	Head     int   // instruction index of the loop head (backedge target)
	Backedge int   // instruction index of the OpJumpBackward
	Parent   int   // index into CFG.Loops of the enclosing loop, or -1
	Children []int // indices into CFG.Loops of directly nested loops
}

// AnalyzeCFG builds the control flow graph and loop tree of a program.
func AnalyzeCFG(program *Program) *CFG {
	code := program.Bytecode
	args := program.Arguments
	cfg := &CFG{}
	if len(code) == 0 {
		return cfg
	}

	// Block leaders: the entry point, every jump target, and every
	// instruction following a jump.
	leaders := map[int]bool{0: true}
	for i, op := range code {
		if !isJump(op) {
			continue
		}
		if op == OpJumpBackward {
			cfg.Loops = append(cfg.Loops, Loop{
				Head:     jumpTarget(i, op, args[i]),
				Backedge: i,
				Parent:   -1,
			})
		}
		if next := i + 1; next < len(code) {
			leaders[next] = true
		}
		if target := jumpTarget(i, op, args[i]); target < len(code) {
			leaders[target] = true
		}
	}

	// Loop bodies are nested intervals: the parent of a loop is the
	// smallest interval strictly containing it.
	for l := range cfg.Loops {
		for m := range cfg.Loops {
			if m == l {
				continue
			}
			if cfg.Loops[m].Head > cfg.Loops[l].Head || cfg.Loops[m].Backedge < cfg.Loops[l].Backedge {
				continue // m does not enclose l
			}
			p := cfg.Loops[l].Parent
			if p == -1 || cfg.Loops[m].Head > cfg.Loops[p].Head {
				cfg.Loops[l].Parent = m
			}
		}
	}
	for l, loop := range cfg.Loops {
		if loop.Parent != -1 {
			parent := &cfg.Loops[loop.Parent]
			parent.Children = append(parent.Children, l)
		}
	}

	starts := make([]int, 0, len(leaders))
	for start := range leaders {
		starts = append(starts, start)
	}
	sort.Ints(starts)

	blockAt := make(map[int]int, len(starts)) // leader instruction index -> block index
	for b, start := range starts {
		end := len(code)
		if b+1 < len(starts) {
			end = starts[b+1]
		}
		cfg.Blocks = append(cfg.Blocks, BasicBlock{Start: start, End: end})
		blockAt[start] = b
	}

	for b := range cfg.Blocks {
		block := &cfg.Blocks[b]
		last := block.End - 1
		op := code[last]

		addSucc := func(target int) {
			if target < len(code) { // a jump to len(code) exits the program
				block.Succs = append(block.Succs, blockAt[target])
			}
		}

		switch {
		case op == OpJump || op == OpJumpBackward:
			addSucc(jumpTarget(last, op, args[last]))
		case isJump(op): // conditional jump: fall through or take the branch
			addSucc(last + 1)
			addSucc(jumpTarget(last, op, args[last]))
		default:
			addSucc(block.End)
		}
	}

	return cfg
}

// PerIterationCost returns the worst-case number of instructions executed by
// one iteration of loop l: the longest path from the loop head to its
// backedge over forward edges only. Nested loops contribute just their
// loop-exit check; their own iterations are charged at their own backedges.
func (cfg *CFG) PerIterationCost(l int) int {
	loop := cfg.Loops[l]
	head := cfg.blockContaining(loop.Head)
	back := cfg.blockContaining(loop.Backedge)

	const unreachable = -1
	dist := make([]int, len(cfg.Blocks))
	for b := range dist {
		dist[b] = unreachable
	}
	dist[head] = cfg.Blocks[head].End - cfg.Blocks[head].Start

	// Dropping backedges leaves a DAG on which block order is already a
	// topological order, so one forward sweep computes longest paths.
	for b := head; b < back; b++ {
		if dist[b] == unreachable {
			continue
		}
		for _, s := range cfg.Blocks[b].Succs {
			if s <= b {
				continue // backedge: not part of a single iteration
			}
			if c := dist[b] + cfg.Blocks[s].End - cfg.Blocks[s].Start; c > dist[s] {
				dist[s] = c
			}
		}
	}
	return dist[back]
}

func (cfg *CFG) blockContaining(ip int) int {
	return sort.Search(len(cfg.Blocks), func(b int) bool {
		return cfg.Blocks[b].End > ip
	})
}

func isJump(op Opcode) bool {
	switch op {
	case OpJump, OpJumpIfTrue, OpJumpIfFalse, OpJumpIfNil,
		OpJumpIfNotNil, OpJumpIfEnd, OpJumpBackward:
		return true
	}
	return false
}

// jumpTarget returns the destination instruction index of a jump at index i.
// The VM increments ip before applying the offset, hence the +1.
func jumpTarget(i int, op Opcode, arg int) int {
	if op == OpJumpBackward {
		return i + 1 - arg
	}
	return i + 1 + arg
}
