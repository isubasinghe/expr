package vm_test

import (
	"testing"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/internal/testify/require"
	"github.com/expr-lang/expr/vm"
)

func TestVM_Fuel_sufficient(t *testing.T) {
	program, err := expr.Compile(`sum(map(1..1000, # * 2))`)
	require.NoError(t, err)

	v := vm.VM{Fuel: 1_000_000}
	out, err := v.Run(program, nil)
	require.NoError(t, err)
	require.Equal(t, 1_001_000, out)
}

func TestVM_Fuel_zeroIsUnlimited(t *testing.T) {
	program, err := expr.Compile(`sum(map(1..10000, # * 2))`)
	require.NoError(t, err)

	v := vm.VM{}
	out, err := v.Run(program, nil)
	require.NoError(t, err)
	require.Equal(t, 100_010_000, out)
}

func TestVM_Fuel_straightLineChargedUpfront(t *testing.T) {
	program, err := expr.Compile(`1 + 2 + 3 + a`)
	require.NoError(t, err)

	v := vm.VM{Fuel: uint(len(program.Bytecode)) - 1}
	_, err = v.Run(program, map[string]any{"a": 4})
	require.Error(t, err)
	require.Contains(t, err.Error(), "fuel budget exceeded")

	v = vm.VM{Fuel: uint(len(program.Bytecode))}
	out, err := v.Run(program, map[string]any{"a": 4})
	require.NoError(t, err)
	require.Equal(t, 10, out)
}

func TestVM_Fuel_resetsBetweenRuns(t *testing.T) {
	program, err := expr.Compile(`sum(map(1..100, #))`)
	require.NoError(t, err)

	v := vm.VM{Fuel: 10_000}
	for i := 0; i < 3; i++ {
		out, err := v.Run(program, nil)
		require.NoError(t, err)
		require.Equal(t, 5050, out)
	}
}

func TestVM_Fuel_exceeded(t *testing.T) {
	program, err := expr.Compile(`sum(map(1..1000, # * 2))`)
	require.NoError(t, err)

	v := vm.VM{Fuel: 100}
	_, err = v.Run(program, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fuel budget exceeded")
}
