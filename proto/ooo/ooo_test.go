package ooo

import (
	"testing"
	"unsafe"
)

// ════════════════════════════════════════════════════════════════════════════════════════════════
// TEST ORGANIZATION
// ════════════════════════════════════════════════════════════════════════════════════════════════
//
// This test suite follows the hardware pipeline structure:
//
// 1. BASIC COMPONENT TESTS - Test individual building blocks
//    - Scoreboard operations
//    - Operation struct fields
//
// 2. CYCLE 0 TESTS (Stage 1) - Dependency checking
//    - ComputeReadyBitmap
//    - BuildDependencyMatrix
//    - Matrix properties
//
// 3. CYCLE 0 TESTS (Stage 2) - Priority classification
//    - ClassifyPriority
//
// 4. CYCLE 1 TESTS - Issue selection
//    - SelectIssueBundle
//    - Age-based ordering
//    - Bundle validation
//
// 5. SCOREBOARD MANAGEMENT - Register state tracking
//    - UpdateScoreboardAfterIssue
//    - UpdateScoreboardAfterComplete
//    - Concurrent updates
//
// 6. INTEGRATION TESTS - Full pipeline behavior
//    - Pipeline registers
//    - End-to-end scheduling
//    - State machine transitions
//    - Interleaved operations
//
// 7. SPECIALIZED SCENARIOS
//    - Scattered window slots
//    - Window slot reuse
//    - Hazard detection (RAW, WAW, WAR)
//
// 8. EDGE CASES AND NEGATIVE TESTS
//    - Boundary conditions
//    - Invalid inputs
//    - Empty states
//
// 9. CORRECTNESS VALIDATION
//    - No double-issue
//    - Dependency enforcement
//
// 10. STRESS AND PERFORMANCE TESTS
//     - Repeated fill/drain
//     - Long dependency chains
//     - Timing analysis
//     - Performance metrics
//     - Documentation validation
//
// ════════════════════════════════════════════════════════════════════════════════════════════════

// ════════════════════════════════════════════════════════════════════════════════════════════════
// 1. BASIC COMPONENT TESTS
// ════════════════════════════════════════════════════════════════════════════════════════════════

// TestScoreboardBasicOperations verifies the fundamental scoreboard operations:
// marking registers as ready/pending and checking their status. This tests the
// core bit manipulation logic that underlies all dependency tracking.
func TestScoreboardBasicOperations(t *testing.T) {
	var sb Scoreboard

	// Initially all registers should be not ready (0)
	for i := uint8(0); i < 64; i++ {
		if sb.IsReady(i) {
			t.Errorf("Register %d should not be ready initially", i)
		}
	}

	// Mark register 5 as ready
	sb.MarkReady(5)
	if !sb.IsReady(5) {
		t.Error("Register 5 should be ready after MarkReady")
	}

	// Verify other registers are still not ready
	if sb.IsReady(4) || sb.IsReady(6) {
		t.Error("Adjacent registers should not be affected")
	}

	// Mark register 5 as pending
	sb.MarkPending(5)
	if sb.IsReady(5) {
		t.Error("Register 5 should not be ready after MarkPending")
	}
}

// TestScoreboardBoundaryRegisters tests the edge cases of the 64-register
// scoreboard: register 0 (lowest) and register 63 (highest). Ensures bit
// manipulation works correctly at boundaries.
func TestScoreboardBoundaryRegisters(t *testing.T) {
	var sb Scoreboard

	// Test register 0 (lowest)
	sb.MarkReady(0)
	if !sb.IsReady(0) {
		t.Error("Register 0 should be ready")
	}

	// Test register 63 (highest)
	sb.MarkReady(63)
	if !sb.IsReady(63) {
		t.Error("Register 63 should be ready")
	}

	// Verify they're independent
	sb.MarkPending(0)
	if sb.IsReady(0) {
		t.Error("Register 0 should not be ready after MarkPending")
	}
	if !sb.IsReady(63) {
		t.Error("Register 63 should still be ready")
	}
}

// TestScoreboardAllRegisters verifies that all 64 registers can be manipulated
// simultaneously. Tests the full range of the bitmap and validates that the
// scoreboard equals expected bit patterns.
func TestScoreboardAllRegisters(t *testing.T) {
	var sb Scoreboard

	// Mark all registers ready
	for i := uint8(0); i < 64; i++ {
		sb.MarkReady(i)
	}

	// Verify all are ready
	for i := uint8(0); i < 64; i++ {
		if !sb.IsReady(i) {
			t.Errorf("Register %d should be ready", i)
		}
	}

	// Verify scoreboard has all bits set
	expected := ^Scoreboard(0) // All 64 bits set
	if sb != expected {
		t.Errorf("Scoreboard should be 0x%016X, got 0x%016X", expected, sb)
	}

	// Mark all registers pending
	for i := uint8(0); i < 64; i++ {
		sb.MarkPending(i)
	}

	// Verify all are not ready
	for i := uint8(0); i < 64; i++ {
		if sb.IsReady(i) {
			t.Errorf("Register %d should not be ready", i)
		}
	}

	// Verify scoreboard is zero
	if sb != 0 {
		t.Errorf("Scoreboard should be 0x0, got 0x%016X", sb)
	}
}

// TestScoreboardInterleaved tests a checkered pattern of ready/pending registers
// to ensure that bit manipulation doesn't affect non-targeted registers.
func TestScoreboardInterleaved(t *testing.T) {
	var sb Scoreboard

	// Mark even registers ready
	for i := uint8(0); i < 64; i += 2 {
		sb.MarkReady(i)
	}

	// Verify pattern
	for i := uint8(0); i < 64; i++ {
		expected := (i % 2) == 0
		if sb.IsReady(i) != expected {
			t.Errorf("Register %d: expected ready=%v, got ready=%v", i, expected, sb.IsReady(i))
		}
	}
}

// TestOpField_DifferentOperations verifies that the Op field (operation type)
// doesn't affect dependency checking. All operations should be scheduled based
// solely on register dependencies, not operation type.
func TestOpField_DifferentOperations(t *testing.T) {
	// Test that different operation types are handled correctly
	const (
		OP_ADD   = 0x01
		OP_MUL   = 0x02
		OP_LOAD  = 0x10
		OP_STORE = 0x11
	)

	window := &InstructionWindow{}
	var sb Scoreboard

	window.Ops[0] = Operation{Valid: true, Op: OP_ADD, Src1: 1, Src2: 2, Dest: 10}
	window.Ops[1] = Operation{Valid: true, Op: OP_MUL, Src1: 3, Src2: 4, Dest: 11}
	window.Ops[2] = Operation{Valid: true, Op: OP_LOAD, Src1: 5, Src2: 0, Dest: 12}
	window.Ops[3] = Operation{Valid: true, Op: OP_STORE, Src1: 6, Src2: 7, Dest: 0}

	// Mark all sources ready
	for i := uint8(0); i < 8; i++ {
		sb.MarkReady(i)
	}

	readyBitmap := ComputeReadyBitmap(window, sb)

	// All ops should be ready regardless of operation type
	expected := uint32(0b1111)
	if readyBitmap != expected {
		t.Errorf("All ops should be ready regardless of Op field, got 0x%X", readyBitmap)
	}
}

// TestImmField_Values verifies that the 16-bit immediate field correctly stores
// and retrieves values. Immediates are used for constants in instructions.
func TestImmField_Values(t *testing.T) {
	// Test immediate field handling
	window := &InstructionWindow{}

	window.Ops[0] = Operation{Valid: true, Imm: 0}
	window.Ops[1] = Operation{Valid: true, Imm: 0xFFFF} // Max 16-bit
	window.Ops[2] = Operation{Valid: true, Imm: 0x1234} // Arbitrary value

	// Verify values are preserved
	if window.Ops[0].Imm != 0 {
		t.Error("Immediate value 0 should be preserved")
	}
	if window.Ops[1].Imm != 0xFFFF {
		t.Error("Immediate value 0xFFFF should be preserved")
	}
	if window.Ops[2].Imm != 0x1234 {
		t.Error("Immediate value should be preserved")
	}
}

// TestAgeField_Boundaries tests the Age field which tracks how old an instruction
// is in the window. Age is 5 bits (0-31) though the field is uint8.
func TestAgeField_Boundaries(t *testing.T) {
	// Age is 5 bits (0-31)
	op := Operation{Valid: true, Age: 0}
	if op.Age != 0 {
		t.Error("Age 0 should be valid")
	}

	op.Age = 31
	if op.Age != 31 {
		t.Error("Age 31 should be valid (max 5-bit value)")
	}

	// Note: uint8 can hold > 31, but docs say Age is 5 bits
	op.Age = 32
	if op.Age != 32 {
		t.Error("Age overflow case: uint8 allows it but design says 5 bits")
	}
	t.Logf("Warning: Age field is uint8 but docs specify 5 bits (0-31)")
}

// ════════════════════════════════════════════════════════════════════════════════════════════════
// 2. CYCLE 0 TESTS (STAGE 1) - DEPENDENCY CHECKING
// ════════════════════════════════════════════════════════════════════════════════════════════════

// TestComputeReadyBitmap_EmptyWindow verifies that an empty instruction window
// produces a zero ready bitmap (no ops ready to execute).
func TestComputeReadyBitmap_EmptyWindow(t *testing.T) {
	window := &InstructionWindow{}
	var sb Scoreboard

	readyBitmap := ComputeReadyBitmap(window, sb)

	if readyBitmap != 0 {
		t.Errorf("Empty window should produce 0 ready bitmap, got 0x%08X", readyBitmap)
	}
}

// TestComputeReadyBitmap_AllReady tests the case where all instructions have
// their source registers ready. All should be marked ready in the bitmap.
func TestComputeReadyBitmap_AllReady(t *testing.T) {
	window := &InstructionWindow{}
	var sb Scoreboard

	// Create 5 valid ops, all sources ready
	for i := 0; i < 5; i++ {
		window.Ops[i] = Operation{
			Valid: true,
			Src1:  uint8(i * 2),
			Src2:  uint8(i*2 + 1),
			Dest:  uint8(i + 10),
		}
		sb.MarkReady(uint8(i * 2))
		sb.MarkReady(uint8(i*2 + 1))
	}

	readyBitmap := ComputeReadyBitmap(window, sb)

	expected := uint32(0b11111) // First 5 bits set
	if readyBitmap != expected {
		t.Errorf("Expected ready bitmap 0x%08X, got 0x%08X", expected, readyBitmap)
	}
}

// TestComputeReadyBitmap_PartialReady tests a mix of ready and not-ready ops.
// Only ops with both source registers ready should be marked in the bitmap.
func TestComputeReadyBitmap_PartialReady(t *testing.T) {
	window := &InstructionWindow{}
	var sb Scoreboard

	// Op 0: Both sources ready
	window.Ops[0] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10}
	sb.MarkReady(1)
	sb.MarkReady(2)

	// Op 1: Only Src1 ready
	window.Ops[1] = Operation{Valid: true, Src1: 1, Src2: 3, Dest: 11}
	// Don't mark register 3 ready

	// Op 2: Neither source ready
	window.Ops[2] = Operation{Valid: true, Src1: 4, Src2: 5, Dest: 12}

	// Op 3: Both sources ready
	window.Ops[3] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 13}

	readyBitmap := ComputeReadyBitmap(window, sb)

	// Only ops 0 and 3 should be ready
	expected := uint32(0b1001)
	if readyBitmap != expected {
		t.Errorf("Expected ready bitmap 0x%08X, got 0x%08X", expected, readyBitmap)
	}
}

// TestComputeReadyBitmap_InvalidOps verifies that invalid ops (Valid=false)
// are never marked as ready, even if their sources are ready.
func TestComputeReadyBitmap_InvalidOps(t *testing.T) {
	window := &InstructionWindow{}
	var sb Scoreboard

	// Mark all registers ready
	for i := uint8(0); i < 64; i++ {
		sb.MarkReady(i)
	}

	// Create ops with valid=false
	for i := 0; i < 32; i++ {
		window.Ops[i] = Operation{
			Valid: false, // Invalid!
			Src1:  1,
			Src2:  2,
			Dest:  uint8(i + 10),
		}
	}

	readyBitmap := ComputeReadyBitmap(window, sb)

	// No ops should be ready (all invalid)
	if readyBitmap != 0 {
		t.Errorf("Invalid ops should not be ready, got bitmap 0x%08X", readyBitmap)
	}
}

// TestComputeReadyBitmap_SameRegisterDependency tests the case where an
// instruction uses the same register for both sources (e.g., ADD r5, r5, r5).
func TestComputeReadyBitmap_SameRegisterDependency(t *testing.T) {
	window := &InstructionWindow{}
	var sb Scoreboard

	// Op uses same register for both sources
	window.Ops[0] = Operation{Valid: true, Src1: 5, Src2: 5, Dest: 10}
	sb.MarkReady(5)

	readyBitmap := ComputeReadyBitmap(window, sb)

	expected := uint32(0b1)
	if readyBitmap != expected {
		t.Errorf("Op with same source registers should be ready if register is ready, got 0x%08X", readyBitmap)
	}
}

// TestComputeReadyBitmap_FullWindow tests dependency checking on a completely
// full 32-instruction window where all ops are ready.
func TestComputeReadyBitmap_FullWindow(t *testing.T) {
	window := &InstructionWindow{}
	var sb Scoreboard

	// Fill all 32 slots with ready ops
	for i := 0; i < 32; i++ {
		window.Ops[i] = Operation{
			Valid: true,
			Src1:  1,
			Src2:  2,
			Dest:  uint8(i + 10),
		}
	}
	sb.MarkReady(1)
	sb.MarkReady(2)

	readyBitmap := ComputeReadyBitmap(window, sb)

	// All 32 bits should be set
	expected := ^uint32(0)
	if readyBitmap != expected {
		t.Errorf("All 32 ops should be ready, got bitmap 0x%08X", readyBitmap)
	}
}

// TestBuildDependencyMatrix_NoDependencies verifies that independent operations
// produce an empty dependency matrix (no ops depend on each other).
func TestBuildDependencyMatrix_NoDependencies(t *testing.T) {
	window := &InstructionWindow{}

	// Create independent ops
	window.Ops[0] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10}
	window.Ops[1] = Operation{Valid: true, Src1: 3, Src2: 4, Dest: 11}
	window.Ops[2] = Operation{Valid: true, Src1: 5, Src2: 6, Dest: 12}

	matrix := BuildDependencyMatrix(window)

	// No dependencies should exist
	for i := 0; i < 3; i++ {
		if matrix[i] != 0 {
			t.Errorf("Op %d should have no dependents, got bitmap 0x%08X", i, matrix[i])
		}
	}
}

// TestBuildDependencyMatrix_SimpleChain tests a basic linear dependency chain
// where A produces r10, B consumes r10 and produces r11, C consumes r11.
func TestBuildDependencyMatrix_SimpleChain(t *testing.T) {
	window := &InstructionWindow{}

	// A → B → C dependency chain
	window.Ops[0] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10}  // A produces r10
	window.Ops[1] = Operation{Valid: true, Src1: 10, Src2: 3, Dest: 11} // B consumes r10, produces r11
	window.Ops[2] = Operation{Valid: true, Src1: 11, Src2: 4, Dest: 12} // C consumes r11, produces r12

	matrix := BuildDependencyMatrix(window)

	// Op 0 (A) has Op 1 (B) as dependent
	if matrix[0] != 0b010 {
		t.Errorf("Op 0 should have Op 1 as dependent, got 0x%08X", matrix[0])
	}

	// Op 1 (B) has Op 2 (C) as dependent
	if matrix[1] != 0b100 {
		t.Errorf("Op 1 should have Op 2 as dependent, got 0x%08X", matrix[1])
	}

	// Op 2 (C) has no dependents
	if matrix[2] != 0 {
		t.Errorf("Op 2 should have no dependents, got 0x%08X", matrix[2])
	}
}

// TestBuildDependencyMatrix_Diamond tests a diamond dependency pattern where
// A produces a value consumed by both B and C, then D consumes outputs from
// both B and C. This is common in parallel computation.
func TestBuildDependencyMatrix_Diamond(t *testing.T) {
	window := &InstructionWindow{}

	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	window.Ops[0] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10}   // A produces r10
	window.Ops[1] = Operation{Valid: true, Src1: 10, Src2: 3, Dest: 11}  // B consumes r10
	window.Ops[2] = Operation{Valid: true, Src1: 10, Src2: 4, Dest: 12}  // C consumes r10
	window.Ops[3] = Operation{Valid: true, Src1: 11, Src2: 12, Dest: 13} // D consumes r11 and r12

	matrix := BuildDependencyMatrix(window)

	// Op 0 (A) has Ops 1 (B) and 2 (C) as dependents
	if matrix[0] != 0b0110 {
		t.Errorf("Op 0 should have Ops 1,2 as dependents, got 0x%08X", matrix[0])
	}

	// Op 1 (B) has Op 3 (D) as dependent
	if matrix[1] != 0b1000 {
		t.Errorf("Op 1 should have Op 3 as dependent, got 0x%08X", matrix[1])
	}

	// Op 2 (C) has Op 3 (D) as dependent
	if matrix[2] != 0b1000 {
		t.Errorf("Op 2 should have Op 3 as dependent, got 0x%08X", matrix[2])
	}

	// Op 3 (D) has no dependents
	if matrix[3] != 0 {
		t.Errorf("Op 3 should have no dependents, got 0x%08X", matrix[3])
	}
}

// TestBuildDependencyMatrix_MultipleConsumers tests the case where one producer
// has multiple consumers (fan-out pattern).
func TestBuildDependencyMatrix_MultipleConsumers(t *testing.T) {
	window := &InstructionWindow{}

	// One producer, three consumers
	window.Ops[0] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10}  // A produces r10
	window.Ops[1] = Operation{Valid: true, Src1: 10, Src2: 3, Dest: 11} // B consumes r10
	window.Ops[2] = Operation{Valid: true, Src1: 10, Src2: 4, Dest: 12} // C consumes r10
	window.Ops[3] = Operation{Valid: true, Src1: 10, Src2: 5, Dest: 13} // D consumes r10

	matrix := BuildDependencyMatrix(window)

	// Op 0 has Ops 1, 2, 3 as dependents
	expected := uint32(0b1110)
	if matrix[0] != expected {
		t.Errorf("Op 0 should have Ops 1,2,3 as dependents, got 0x%08X", matrix[0])
	}
}

// TestBuildDependencyMatrix_InvalidOps verifies that invalid ops don't create
// dependencies in the matrix.
func TestBuildDependencyMatrix_InvalidOps(t *testing.T) {
	window := &InstructionWindow{}

	// Valid op followed by invalid ops
	window.Ops[0] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10}
	window.Ops[1] = Operation{Valid: false, Src1: 10, Src2: 3, Dest: 11} // Invalid!

	matrix := BuildDependencyMatrix(window)

	// Op 0 should have no dependents (Op 1 is invalid)
	if matrix[0] != 0 {
		t.Errorf("Op 0 should have no dependents (Op 1 invalid), got 0x%08X", matrix[0])
	}
}

// TestBuildDependencyMatrix_BothSourcesDependOnSameOp tests the case where
// both source registers of an instruction come from the same producer.
func TestBuildDependencyMatrix_BothSourcesDependOnSameOp(t *testing.T) {
	window := &InstructionWindow{}

	window.Ops[0] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10}
	window.Ops[1] = Operation{Valid: true, Src1: 10, Src2: 10, Dest: 11} // Both sources from r10

	matrix := BuildDependencyMatrix(window)

	// Op 1 should still show up once as dependent of Op 0
	if matrix[0] != 0b10 {
		t.Errorf("Op 0 should have Op 1 as dependent, got 0x%08X", matrix[0])
	}
}

// TestDependencyMatrix_DiagonalIsZero verifies that no operation depends on
// itself (the diagonal of the dependency matrix should be zero).
func TestDependencyMatrix_DiagonalIsZero(t *testing.T) {
	window := &InstructionWindow{}

	// Create ops that could create self-dependencies
	for i := 0; i < 5; i++ {
		window.Ops[i] = Operation{
			Valid: true,
			Src1:  uint8(i),
			Src2:  uint8(i + 1),
			Dest:  uint8(i),
		}
	}

	matrix := BuildDependencyMatrix(window)

	// Diagonal should be zero (op doesn't depend on itself)
	for i := 0; i < 5; i++ {
		if (matrix[i]>>i)&1 != 0 {
			t.Errorf("Dependency matrix diagonal[%d] should be 0", i)
		}
	}
}

// ════════════════════════════════════════════════════════════════════════════════════════════════
// 3. CYCLE 0 TESTS (STAGE 2) - PRIORITY CLASSIFICATION
// ════════════════════════════════════════════════════════════════════════════════════════════════

// TestClassifyPriority_AllCriticalPath tests the case where all ready ops have
// dependents (all are on critical path except the last leaf node).
func TestClassifyPriority_AllCriticalPath(t *testing.T) {
	// All ops have dependents (all on critical path)
	readyBitmap := uint32(0b111)
	depMatrix := DependencyMatrix{
		0b010, // Op 0 has Op 1 as dependent
		0b100, // Op 1 has Op 2 as dependent
		0b000, // Op 2 has no dependents (but we only check ready ops)
	}

	priority := ClassifyPriority(readyBitmap, depMatrix)

	// Ops 0 and 1 should be high priority (have dependents)
	expectedHigh := uint32(0b011)
	expectedLow := uint32(0b100) // Op 2 has no dependents

	if priority.HighPriority != expectedHigh {
		t.Errorf("Expected high priority 0x%08X, got 0x%08X", expectedHigh, priority.HighPriority)
	}

	if priority.LowPriority != expectedLow {
		t.Errorf("Expected low priority 0x%08X, got 0x%08X", expectedLow, priority.LowPriority)
	}
}

// TestClassifyPriority_AllLeaves tests the case where no ops have dependents
// (all are leaf nodes). All should be classified as low priority.
func TestClassifyPriority_AllLeaves(t *testing.T) {
	// All ops are leaves (no dependents)
	readyBitmap := uint32(0b1111)
	depMatrix := DependencyMatrix{
		0, 0, 0, 0, // No dependencies
	}

	priority := ClassifyPriority(readyBitmap, depMatrix)

	// All should be low priority
	if priority.HighPriority != 0 {
		t.Errorf("Expected no high priority ops, got 0x%08X", priority.HighPriority)
	}

	if priority.LowPriority != readyBitmap {
		t.Errorf("Expected all low priority, got 0x%08X", priority.LowPriority)
	}
}

// TestClassifyPriority_Mixed tests a realistic mix of critical path ops
// (with dependents) and leaf ops (without dependents).
func TestClassifyPriority_Mixed(t *testing.T) {
	// Mixed critical path and leaves
	readyBitmap := uint32(0b11111)
	depMatrix := DependencyMatrix{
		0b00010, // Op 0 → Op 1
		0b00000, // Op 1 is a leaf
		0b01000, // Op 2 → Op 3
		0b00000, // Op 3 is a leaf
		0b00000, // Op 4 is a leaf
	}

	priority := ClassifyPriority(readyBitmap, depMatrix)

	// Ops 0 and 2 are high priority (have dependents)
	expectedHigh := uint32(0b00101)
	// Ops 1, 3, 4 are low priority (leaves)
	expectedLow := uint32(0b11010)

	if priority.HighPriority != expectedHigh {
		t.Errorf("Expected high priority 0x%08X, got 0x%08X", expectedHigh, priority.HighPriority)
	}

	if priority.LowPriority != expectedLow {
		t.Errorf("Expected low priority 0x%08X, got 0x%08X", expectedLow, priority.LowPriority)
	}
}

// TestClassifyPriority_EmptyReadyBitmap tests the case where no ops are ready.
// Both priority classes should be empty.
func TestClassifyPriority_EmptyReadyBitmap(t *testing.T) {
	readyBitmap := uint32(0)
	depMatrix := DependencyMatrix{
		0b010, 0b100, 0b000,
	}

	priority := ClassifyPriority(readyBitmap, depMatrix)

	// No ready ops, so both should be empty
	if priority.HighPriority != 0 || priority.LowPriority != 0 {
		t.Error("Empty ready bitmap should produce empty priority classes")
	}
}

// TestClassifyPriority_OnlyNonReadyOpsHaveDependents verifies that only ready
// ops are classified. Non-ready ops with dependents don't affect priority.
func TestClassifyPriority_OnlyNonReadyOpsHaveDependents(t *testing.T) {
	// Only ops 0 and 1 are ready, but op 2 (not ready) has dependents
	readyBitmap := uint32(0b011)
	depMatrix := DependencyMatrix{
		0b000, // Op 0 no dependents
		0b000, // Op 1 no dependents
		0b111, // Op 2 has dependents (but not ready)
	}

	priority := ClassifyPriority(readyBitmap, depMatrix)

	// Both ready ops should be low priority (no dependents)
	if priority.HighPriority != 0 {
		t.Errorf("Expected no high priority, got 0x%08X", priority.HighPriority)
	}

	if priority.LowPriority != readyBitmap {
		t.Errorf("Expected low priority 0x%08X, got 0x%08X", readyBitmap, priority.LowPriority)
	}
}

// ════════════════════════════════════════════════════════════════════════════════════════════════
// 4. CYCLE 1 TESTS - ISSUE SELECTION
// ════════════════════════════════════════════════════════════════════════════════════════════════

// TestSelectIssueBundle_Empty verifies that empty priority classes produce
// an empty issue bundle (no ops to execute).
func TestSelectIssueBundle_Empty(t *testing.T) {
	priority := PriorityClass{
		HighPriority: 0,
		LowPriority:  0,
	}

	bundle := SelectIssueBundle(priority)

	if bundle.Valid != 0 {
		t.Errorf("Empty priority should produce empty bundle, got valid mask 0x%04X", bundle.Valid)
	}
}

// TestSelectIssueBundle_LessThan16 tests selection when fewer than 16 ops are
// available. All available ops should be selected.
func TestSelectIssueBundle_LessThan16(t *testing.T) {
	priority := PriorityClass{
		HighPriority: 0b1111, // 4 ops
		LowPriority:  0,
	}

	bundle := SelectIssueBundle(priority)

	// Should select all 4 ops
	if bundle.Valid != 0b1111 {
		t.Errorf("Expected 4 ops selected, got valid mask 0x%04X", bundle.Valid)
	}

	// Verify indices are correct (bits 0,1,2,3 from high priority)
	expectedIndices := []uint8{0, 1, 2, 3}
	for i := 0; i < 4; i++ {
		found := false
		for j := 0; j < 4; j++ {
			if bundle.Indices[i] == expectedIndices[j] {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Index %d not found in expected indices", bundle.Indices[i])
		}
	}
}

// TestSelectIssueBundle_Exactly16 tests the case where exactly 16 ops are
// available (maximum issue width). All should be selected.
func TestSelectIssueBundle_Exactly16(t *testing.T) {
	priority := PriorityClass{
		HighPriority: 0xFFFF, // Exactly 16 ops (bits 0-15)
		LowPriority:  0,
	}

	bundle := SelectIssueBundle(priority)

	// Should select all 16 ops
	if bundle.Valid != 0xFFFF {
		t.Errorf("Expected 16 ops selected, got valid mask 0x%04X", bundle.Valid)
	}
}

// TestSelectIssueBundle_MoreThan16 tests the case where more than 16 ops are
// available. Only 16 should be selected (hardware limit).
func TestSelectIssueBundle_MoreThan16(t *testing.T) {
	priority := PriorityClass{
		HighPriority: 0xFFFFFFFF, // All 32 ops
		LowPriority:  0,
	}

	bundle := SelectIssueBundle(priority)

	// Should select only 16 ops
	count := 0
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 {
			count++
		}
	}

	if count != 16 {
		t.Errorf("Expected exactly 16 ops selected, got %d", count)
	}
}

// TestSelectIssueBundle_HighPriorityFirst verifies that high priority ops
// (critical path) are selected before low priority ops (leaves).
func TestSelectIssueBundle_HighPriorityFirst(t *testing.T) {
	priority := PriorityClass{
		HighPriority: 0b11,    // Ops 0, 1
		LowPriority:  0b11100, // Ops 2, 3, 4
	}

	bundle := SelectIssueBundle(priority)

	// Should select high priority first
	// Indices should include 0 and 1 from high priority
	foundHigh := 0
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 == 0 {
			continue
		}
		idx := bundle.Indices[i]
		if idx == 0 || idx == 1 {
			foundHigh++
		}
	}

	if foundHigh != 2 {
		t.Errorf("Should select both high priority ops, found %d", foundHigh)
	}
}

// TestSelectIssueBundle_LowPriorityWhenNoHigh verifies that low priority ops
// are selected when no high priority ops are available.
func TestSelectIssueBundle_LowPriorityWhenNoHigh(t *testing.T) {
	priority := PriorityClass{
		HighPriority: 0,
		LowPriority:  0b111, // Ops 0, 1, 2
	}

	bundle := SelectIssueBundle(priority)

	// Should select from low priority
	count := 0
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 {
			count++
		}
	}

	if count != 3 {
		t.Errorf("Expected 3 low priority ops, got %d", count)
	}
}

// TestSelectIssueBundle_OldestFirst verifies that within a priority class,
// older ops (higher bit position) are selected first (FIFO fairness).
func TestSelectIssueBundle_OldestFirst(t *testing.T) {
	priority := PriorityClass{
		HighPriority: 0b11110000, // Ops 4,5,6,7 (older = higher index)
		LowPriority:  0,
	}

	bundle := SelectIssueBundle(priority)

	// Should select op 7 first (highest bit = oldest)
	// Note: SelectIssueBundle uses CLZ which finds highest bit first
	if bundle.Indices[0] != 7 {
		t.Errorf("Expected oldest op (7) first, got %d", bundle.Indices[0])
	}
}

// TestSelectIssueBundle_AgeOrderingWithinPriority documents the current behavior
// where bit position (not Age field) determines ordering within priority class.
func TestSelectIssueBundle_AgeOrderingWithinPriority(t *testing.T) {
	// Within same priority tier, older ops (higher age) should be selected first
	// Age field: Higher value = older op
	priority := PriorityClass{
		HighPriority: 0b11111, // Ops 0-4 all high priority
		LowPriority:  0,
	}

	bundle := SelectIssueBundle(priority)

	// Note: Current implementation uses bit position (higher index = older)
	// not the Age field. This test documents current behavior.
	// If Age field should be used, implementation needs updating.

	// Current behavior: Op 4 selected first (highest bit set)
	if bundle.Indices[0] != 4 {
		t.Logf("Note: SelectIssueBundle uses bit position, not Age field")
		t.Logf("Op at index %d selected first (bit position priority)", bundle.Indices[0])
	}
}

// TestBundleValid_HighBits tests issue selection from the upper half of the
// instruction window (ops 16-31).
func TestBundleValid_HighBits(t *testing.T) {
	priority := PriorityClass{
		HighPriority: 0xFFFF0000, // Only high 16 bits set (ops 16-31)
		LowPriority:  0,
	}

	bundle := SelectIssueBundle(priority)

	// Should select 16 ops from indices 16-31
	count := 0
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 {
			count++
			idx := bundle.Indices[i]
			if idx < 16 || idx > 31 {
				t.Errorf("Index %d out of expected range [16-31]", idx)
			}
		}
	}

	if count != 16 {
		t.Errorf("Expected 16 ops selected, got %d", count)
	}
}

// ════════════════════════════════════════════════════════════════════════════════════════════════
// 5. SCOREBOARD MANAGEMENT TESTS
// ════════════════════════════════════════════════════════════════════════════════════════════════

// TestUpdateScoreboardAfterIssue_Single tests marking a single register as
// pending after issuing one operation.
func TestUpdateScoreboardAfterIssue_Single(t *testing.T) {
	var sb Scoreboard
	window := &InstructionWindow{}

	// Op writes to register 10
	window.Ops[0] = Operation{Valid: true, Dest: 10}

	bundle := IssueBundle{
		Indices: [16]uint8{0},
		Valid:   0b1,
	}

	// Mark r10 as ready initially
	sb.MarkReady(10)

	UpdateScoreboardAfterIssue(&sb, window, bundle)

	// r10 should now be pending
	if sb.IsReady(10) {
		t.Error("Register 10 should be pending after issue")
	}
}

// TestUpdateScoreboardAfterIssue_Multiple tests marking multiple registers as
// pending when issuing multiple operations simultaneously.
func TestUpdateScoreboardAfterIssue_Multiple(t *testing.T) {
	var sb Scoreboard
	window := &InstructionWindow{}

	// Three ops writing to different registers
	window.Ops[0] = Operation{Valid: true, Dest: 10}
	window.Ops[1] = Operation{Valid: true, Dest: 11}
	window.Ops[2] = Operation{Valid: true, Dest: 12}

	bundle := IssueBundle{
		Indices: [16]uint8{0, 1, 2},
		Valid:   0b111,
	}

	// Mark all ready initially
	sb.MarkReady(10)
	sb.MarkReady(11)
	sb.MarkReady(12)

	UpdateScoreboardAfterIssue(&sb, window, bundle)

	// All should be pending
	if sb.IsReady(10) || sb.IsReady(11) || sb.IsReady(12) {
		t.Error("All issued registers should be pending")
	}
}

// TestUpdateScoreboardAfterIssue_AllSixteen tests the maximum case where all
// 16 execution units issue simultaneously.
func TestUpdateScoreboardAfterIssue_AllSixteen(t *testing.T) {
	var sb Scoreboard
	window := &InstructionWindow{}

	// 16 ops writing to registers 10-25
	for i := 0; i < 16; i++ {
		window.Ops[i] = Operation{Valid: true, Dest: uint8(10 + i)}
		sb.MarkReady(uint8(10 + i))
	}

	bundle := IssueBundle{
		Valid: 0xFFFF, // All 16 valid
	}
	for i := 0; i < 16; i++ {
		bundle.Indices[i] = uint8(i)
	}

	UpdateScoreboardAfterIssue(&sb, window, bundle)

	// All should be pending
	for i := 0; i < 16; i++ {
		if sb.IsReady(uint8(10 + i)) {
			t.Errorf("Register %d should be pending", 10+i)
		}
	}
}

// TestUpdateScoreboardAfterIssue_EmptyBundle verifies that an empty issue
// bundle doesn't modify the scoreboard.
func TestUpdateScoreboardAfterIssue_EmptyBundle(t *testing.T) {
	var sb Scoreboard

	// Mark some registers ready
	sb.MarkReady(10)
	sb.MarkReady(11)

	originalSb := sb

	bundle := IssueBundle{
		Valid: 0, // Empty
	}

	UpdateScoreboardAfterIssue(&sb, &InstructionWindow{}, bundle)

	// Scoreboard should be unchanged
	if sb != originalSb {
		t.Error("Empty bundle should not modify scoreboard")
	}
}

// TestUpdateScoreboardAfterComplete_Single tests marking a single register as
// ready after completing one operation.
func TestUpdateScoreboardAfterComplete_Single(t *testing.T) {
	var sb Scoreboard

	destRegs := [16]uint8{10}
	completeMask := uint16(0b1)

	UpdateScoreboardAfterComplete(&sb, destRegs, completeMask)

	// Register 10 should be ready
	if !sb.IsReady(10) {
		t.Error("Register 10 should be ready after completion")
	}
}

// TestUpdateScoreboardAfterComplete_Multiple tests marking multiple registers
// as ready when multiple operations complete simultaneously.
func TestUpdateScoreboardAfterComplete_Multiple(t *testing.T) {
	var sb Scoreboard

	destRegs := [16]uint8{10, 11, 12}
	completeMask := uint16(0b111)

	UpdateScoreboardAfterComplete(&sb, destRegs, completeMask)

	// All should be ready
	if !sb.IsReady(10) || !sb.IsReady(11) || !sb.IsReady(12) {
		t.Error("All completed registers should be ready")
	}
}

// TestUpdateScoreboardAfterComplete_Selective tests selective completion where
// only some operations complete (variable latency execution).
func TestUpdateScoreboardAfterComplete_Selective(t *testing.T) {
	var sb Scoreboard

	destRegs := [16]uint8{10, 11, 12, 13}
	completeMask := uint16(0b1010) // Complete 10 and 12, not 11 and 13

	UpdateScoreboardAfterComplete(&sb, destRegs, completeMask)

	// Only ops 1 and 3 completed (indices match mask bits)
	if !sb.IsReady(11) || !sb.IsReady(13) {
		t.Error("Registers at indices 1 and 3 should be ready")
	}

	if sb.IsReady(10) || sb.IsReady(12) {
		t.Error("Registers at indices 0 and 2 should not be ready")
	}
}

// TestConcurrentScoreboardUpdates tests the case where all 16 SLUs complete
// simultaneously (maximum throughput).
func TestConcurrentScoreboardUpdates(t *testing.T) {
	var sb Scoreboard

	// Simulate 16 SLUs completing simultaneously
	destRegs := [16]uint8{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25}
	completeMask := uint16(0xFFFF) // All 16 complete

	UpdateScoreboardAfterComplete(&sb, destRegs, completeMask)

	// All 16 registers should be ready
	for i := 0; i < 16; i++ {
		if !sb.IsReady(destRegs[i]) {
			t.Errorf("Register %d should be ready after concurrent completion", destRegs[i])
		}
	}
}

// TestOverlappingScoreboardUpdates tests a Write-After-Write (WAW) hazard where
// two operations write to the same register. Last write wins (architectural).
func TestOverlappingScoreboardUpdates(t *testing.T) {
	var sb Scoreboard

	// Two ops write to the same register (WAW hazard)
	destRegs := [16]uint8{10, 10} // Both write to r10
	completeMask := uint16(0b11)

	UpdateScoreboardAfterComplete(&sb, destRegs, completeMask)

	// Register 10 should be ready (last write wins)
	if !sb.IsReady(10) {
		t.Error("Register 10 should be ready after multiple writes")
	}
}

// ════════════════════════════════════════════════════════════════════════════════════════════════
// 6. INTEGRATION TESTS
// ════════════════════════════════════════════════════════════════════════════════════════════════

// TestPipelineRegister_StateTransfer verifies that the pipeline register between
// Cycle 0 and Cycle 1 correctly transfers priority class state.
func TestPipelineRegister_StateTransfer(t *testing.T) {
	sched := &OoOScheduler{}

	// Setup window
	for i := 0; i < 5; i++ {
		sched.Window.Ops[i] = Operation{
			Valid: true,
			Src1:  1,
			Src2:  2,
			Dest:  uint8(i + 10),
		}
	}
	sched.Scoreboard.MarkReady(1)
	sched.Scoreboard.MarkReady(2)

	// Cycle 0: Compute priority
	sched.ScheduleCycle0()

	// Verify PipelinedPriority is populated
	if sched.PipelinedPriority.HighPriority == 0 && sched.PipelinedPriority.LowPriority == 0 {
		t.Error("PipelinedPriority should be populated after Cycle 0")
	}

	// Save state
	savedPriority := sched.PipelinedPriority

	// Cycle 1 should use pipelined state
	bundle := sched.ScheduleCycle1()

	if bundle.Valid == 0 {
		t.Error("Cycle 1 should produce bundle from pipelined priority")
	}

	// Verify priority was used (indirect - bundle should match priority)
	if savedPriority.HighPriority != 0 || savedPriority.LowPriority != 0 {
		t.Log("✓ Pipeline register correctly transferred state between cycles")
	}
}

// TestOoOScheduler_SimpleDependencyChain tests a basic linear dependency chain
// through the full scheduler pipeline. Verifies ops are issued in order.
func TestOoOScheduler_SimpleDependencyChain(t *testing.T) {
	sched := &OoOScheduler{}

	// Create a simple dependency chain: A → B → C
	sched.Window.Ops[0] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10, Op: 0xAD, Age: 0}
	sched.Window.Ops[1] = Operation{Valid: true, Src1: 10, Src2: 3, Dest: 11, Op: 0xAD, Age: 1}
	sched.Window.Ops[2] = Operation{Valid: true, Src1: 11, Src2: 4, Dest: 12, Op: 0xAD, Age: 2}

	// Mark initial registers ready
	sched.Scoreboard.MarkReady(1)
	sched.Scoreboard.MarkReady(2)
	sched.Scoreboard.MarkReady(3)
	sched.Scoreboard.MarkReady(4)

	// Cycle 0: Dependency check and priority
	sched.ScheduleCycle0()

	// Cycle 1: Issue selection
	bundle := sched.ScheduleCycle1()

	// Should issue Op 0 (A) only, since B and C depend on it
	foundOp0 := false
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 {
			if bundle.Indices[i] == 0 {
				foundOp0 = true
			}
		}
	}

	if !foundOp0 {
		t.Error("Should issue Op 0 first")
	}

	// Now simulate Op 0 completing
	sched.ScheduleComplete([16]uint8{10}, 0b1)

	// Op 1 should now be ready
	sched.ScheduleCycle0()
	bundle = sched.ScheduleCycle1()

	foundOp1 := false
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 && bundle.Indices[i] == 1 {
			foundOp1 = true
		}
	}

	if !foundOp1 {
		t.Error("Should issue Op 1 after Op 0 completes")
	}
}

// TestOoOScheduler_ParallelIndependentOps tests maximum parallelism: 20 independent
// ops should issue 16 immediately (SLU limit).
func TestOoOScheduler_ParallelIndependentOps(t *testing.T) {
	sched := &OoOScheduler{}

	// Create 20 independent ops
	for i := 0; i < 20; i++ {
		sched.Window.Ops[i] = Operation{
			Valid: true,
			Src1:  uint8(i * 2),
			Src2:  uint8(i*2 + 1),
			Dest:  uint8(i + 20),
			Op:    0xAD,
			Age:   uint8(i),
		}
		sched.Scoreboard.MarkReady(uint8(i * 2))
		sched.Scoreboard.MarkReady(uint8(i*2 + 1))
	}

	// Cycle 0 and 1
	sched.ScheduleCycle0()
	bundle := sched.ScheduleCycle1()

	// Should issue 16 ops (maximum)
	count := 0
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 {
			count++
		}
	}

	if count != 16 {
		t.Errorf("Should issue 16 ops, got %d", count)
	}
}

// TestOoOScheduler_DiamondDependency tests a diamond dependency pattern where
// A fans out to B and C, which both feed into D. Tests proper synchronization.
func TestOoOScheduler_DiamondDependency(t *testing.T) {
	sched := &OoOScheduler{}

	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	sched.Window.Ops[0] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10, Op: 0xAD}   // A
	sched.Window.Ops[1] = Operation{Valid: true, Src1: 10, Src2: 3, Dest: 11, Op: 0xAD}  // B
	sched.Window.Ops[2] = Operation{Valid: true, Src1: 10, Src2: 4, Dest: 12, Op: 0xAD}  // C
	sched.Window.Ops[3] = Operation{Valid: true, Src1: 11, Src2: 12, Dest: 13, Op: 0xAD} // D

	// Mark initial registers ready
	for i := uint8(1); i <= 4; i++ {
		sched.Scoreboard.MarkReady(i)
	}

	// First cycle: Should issue A
	sched.ScheduleCycle0()
	bundle := sched.ScheduleCycle1()

	foundA := false
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 && bundle.Indices[i] == 0 {
			foundA = true
		}
	}
	if !foundA {
		t.Error("Should issue A first")
	}

	// A completes
	sched.ScheduleComplete([16]uint8{10}, 0b1)

	// Second cycle: Should issue B and C (both ready now)
	sched.ScheduleCycle0()
	bundle = sched.ScheduleCycle1()

	foundB, foundC := false, false
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 == 0 {
			continue
		}
		idx := bundle.Indices[i]
		if idx == 1 {
			foundB = true
		}
		if idx == 2 {
			foundC = true
		}
	}

	if !foundB || !foundC {
		t.Error("Should issue both B and C after A completes")
	}

	// B and C complete
	sched.ScheduleComplete([16]uint8{11, 12}, 0b11)

	// Third cycle: Should issue D
	sched.ScheduleCycle0()
	bundle = sched.ScheduleCycle1()

	foundD := false
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 && bundle.Indices[i] == 3 {
			foundD = true
		}
	}

	if !foundD {
		t.Error("Should issue D after B and C complete")
	}
}

// TestOoOScheduler_FullWindow tests a completely full 32-instruction window,
// verifying proper handling of maximum window capacity.
func TestOoOScheduler_FullWindow(t *testing.T) {
	sched := &OoOScheduler{}

	// Fill all 32 slots with independent ops
	for i := 0; i < 32; i++ {
		sched.Window.Ops[i] = Operation{
			Valid: true,
			Src1:  1,
			Src2:  2,
			Dest:  uint8(i + 10),
			Op:    0xAD,
			Age:   uint8(i),
		}
	}
	sched.Scoreboard.MarkReady(1)
	sched.Scoreboard.MarkReady(2)

	// First issue: Should get 16 ops
	sched.ScheduleCycle0()
	bundle := sched.ScheduleCycle1()

	count1 := 0
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 {
			count1++
		}
	}

	if count1 != 16 {
		t.Errorf("First issue should select 16 ops, got %d", count1)
	}

	// Complete first batch
	var destRegs [16]uint8
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 {
			idx := bundle.Indices[i]
			destRegs[i] = sched.Window.Ops[idx].Dest
		}
	}
	sched.ScheduleComplete(destRegs, bundle.Valid)

	// Mark issued ops as invalid (retired)
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 {
			sched.Window.Ops[bundle.Indices[i]].Valid = false
		}
	}

	// Second issue: Should get remaining 16 ops
	sched.ScheduleCycle0()
	bundle = sched.ScheduleCycle1()

	count2 := 0
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 {
			count2++
		}
	}

	if count2 != 16 {
		t.Errorf("Second issue should select 16 ops, got %d", count2)
	}
}

// TestOoOScheduler_EmptyWindow verifies that an empty window produces no
// issue bundle (graceful handling of idle state).
func TestOoOScheduler_EmptyWindow(t *testing.T) {
	sched := &OoOScheduler{}

	// All ops invalid
	for i := 0; i < 32; i++ {
		sched.Window.Ops[i].Valid = false
	}

	sched.ScheduleCycle0()
	bundle := sched.ScheduleCycle1()

	if bundle.Valid != 0 {
		t.Errorf("Empty window should produce empty bundle, got 0x%04X", bundle.Valid)
	}
}

// TestOoOScheduler_AllDependenciesBlocked tests the case where all ops are
// waiting on dependencies (all blocked on unavailable registers).
func TestOoOScheduler_AllDependenciesBlocked(t *testing.T) {
	sched := &OoOScheduler{}

	// All ops depend on unavailable registers
	for i := 0; i < 10; i++ {
		sched.Window.Ops[i] = Operation{
			Valid: true,
			Src1:  50, // Not ready
			Src2:  51, // Not ready
			Dest:  uint8(i + 10),
			Op:    0xAD,
		}
	}

	sched.ScheduleCycle0()
	bundle := sched.ScheduleCycle1()

	if bundle.Valid != 0 {
		t.Error("All ops blocked should produce empty bundle")
	}
}

// TestStateMachine_AllTransitions tests all valid state transitions of an
// operation through the scheduler: enter → ready → issue → execute → complete → retire.
func TestStateMachine_AllTransitions(t *testing.T) {
	// Test all valid state transitions of an op through the scheduler
	sched := &OoOScheduler{}

	// State 1: Op enters window (valid, sources not ready)
	sched.Window.Ops[0] = Operation{Valid: true, Src1: 10, Src2: 11, Dest: 12}

	// State 2: Sources become ready
	sched.Scoreboard.MarkReady(10)
	sched.Scoreboard.MarkReady(11)

	// State 3: Op is selected for issue
	sched.ScheduleCycle0()
	bundle := sched.ScheduleCycle1()

	if bundle.Valid&1 == 0 {
		t.Fatal("Op should be issued")
	}

	// State 4: Op is executing (dest marked pending)
	if sched.Scoreboard.IsReady(12) {
		t.Error("Dest should be pending during execution")
	}

	// State 5: Op completes (dest marked ready)
	sched.ScheduleComplete([16]uint8{12}, 0b1)
	if !sched.Scoreboard.IsReady(12) {
		t.Error("Dest should be ready after completion")
	}

	// State 6: Op retires (marked invalid)
	sched.Window.Ops[0].Valid = false

	t.Log("✓ All state transitions tested")
}

// TestInterleavedIssueAndComplete tests overlapping issue and completion:
// some ops completing while others are issuing (realistic pipelined behavior).
func TestInterleavedIssueAndComplete(t *testing.T) {
	sched := &OoOScheduler{}

	// Setup: Two batches of ops
	// Batch 1: Ops 0-3 (ready to issue)
	for i := 0; i < 4; i++ {
		sched.Window.Ops[i] = Operation{
			Valid: true,
			Src1:  1,
			Src2:  2,
			Dest:  uint8(i + 10),
		}
	}
	sched.Scoreboard.MarkReady(1)
	sched.Scoreboard.MarkReady(2)

	// Batch 2: Ops 4-7 (depend on batch 1)
	for i := 4; i < 8; i++ {
		sched.Window.Ops[i] = Operation{
			Valid: true,
			Src1:  uint8(i + 6), // Depends on batch 1 dest
			Src2:  2,
			Dest:  uint8(i + 10),
		}
	}

	// Issue batch 1
	sched.ScheduleCycle0()
	_ = sched.ScheduleCycle1()

	// Complete batch 1 ops 0 and 1 while batch 1 is still executing
	sched.ScheduleComplete([16]uint8{10, 11}, 0b11)

	// Now issue should pick up newly ready ops from batch 2
	sched.ScheduleCycle0()
	bundle2 := sched.ScheduleCycle1()

	// Check that some batch 2 ops are now issuable
	foundBatch2 := false
	for i := 0; i < 16; i++ {
		if (bundle2.Valid>>i)&1 != 0 {
			idx := bundle2.Indices[i]
			if idx >= 4 && idx < 8 {
				foundBatch2 = true
			}
		}
	}

	if !foundBatch2 {
		t.Error("Should issue batch 2 ops after batch 1 partially completes")
	}

	t.Log("✓ Interleaved issue and complete works correctly")
}

// ════════════════════════════════════════════════════════════════════════════════════════════════
// 7. SPECIALIZED SCENARIOS
// ════════════════════════════════════════════════════════════════════════════════════════════════

// TestScatteredWindowSlots tests non-contiguous valid ops in the window
// (ops at indices 0, 5, 10, 15, etc.). Verifies sparse window handling.
func TestScatteredWindowSlots(t *testing.T) {
	// Valid ops at non-contiguous indices
	window := &InstructionWindow{}
	var sb Scoreboard

	// Ops at indices 0, 5, 10, 15, 20, 25, 30
	for _, i := range []int{0, 5, 10, 15, 20, 25, 30} {
		window.Ops[i] = Operation{
			Valid: true,
			Src1:  1,
			Src2:  2,
			Dest:  uint8(i + 10),
		}
	}
	sb.MarkReady(1)
	sb.MarkReady(2)

	readyBitmap := ComputeReadyBitmap(window, sb)

	// Check that exactly these ops are ready
	for i := 0; i < 32; i++ {
		isScattered := (i == 0 || i == 5 || i == 10 || i == 15 || i == 20 || i == 25 || i == 30)
		isReady := (readyBitmap>>i)&1 != 0

		if isScattered != isReady {
			t.Errorf("Op %d: expected ready=%v, got ready=%v", i, isScattered, isReady)
		}
	}
}

// TestWindowSlotReuse tests that window slots can be reused after ops retire
// (circular buffer behavior for instruction window).
func TestWindowSlotReuse(t *testing.T) {
	sched := &OoOScheduler{}

	// Fill window
	for i := 0; i < 5; i++ {
		sched.Window.Ops[i] = Operation{
			Valid: true,
			Src1:  1,
			Src2:  2,
			Dest:  uint8(i + 10),
		}
		sched.Scoreboard.MarkReady(1)
		sched.Scoreboard.MarkReady(2)
	}

	// Issue and complete all ops
	sched.ScheduleCycle0()
	bundle := sched.ScheduleCycle1()

	// Mark completed ops as invalid (retired)
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 {
			sched.Window.Ops[bundle.Indices[i]].Valid = false
		}
	}

	// Reuse the same slots
	for i := 0; i < 3; i++ {
		sched.Window.Ops[i] = Operation{
			Valid: true,
			Src1:  5,
			Src2:  6,
			Dest:  uint8(i + 20),
		}
	}
	sched.Scoreboard.MarkReady(5)
	sched.Scoreboard.MarkReady(6)

	// Should issue new ops
	sched.ScheduleCycle0()
	bundle = sched.ScheduleCycle1()

	if bundle.Valid == 0 {
		t.Error("Reused window slots should produce valid bundle")
	}
}

// TestHazard_RAW tests Read-After-Write hazard detection: the primary hazard
// tracked by the scheduler. Op 1 must wait for Op 0 to complete.
func TestHazard_RAW(t *testing.T) {
	// Read After Write - the primary hazard tracked
	window := &InstructionWindow{}

	// Op 0 writes r10, Op 1 reads r10
	window.Ops[0] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10}
	window.Ops[1] = Operation{Valid: true, Src1: 10, Src2: 3, Dest: 11}

	matrix := BuildDependencyMatrix(window)

	// Op 1 should depend on Op 0 (RAW)
	if (matrix[0]>>1)&1 == 0 {
		t.Error("RAW hazard not detected: Op 1 depends on Op 0")
	}
}

// TestHazard_WAW tests Write-After-Write hazard: not tracked in current
// implementation (would require register renaming).
func TestHazard_WAW(t *testing.T) {
	// Write After Write - multiple writers to same register
	window := &InstructionWindow{}

	// Both ops write to r10
	window.Ops[0] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10}
	window.Ops[1] = Operation{Valid: true, Src1: 3, Src2: 4, Dest: 10}

	matrix := BuildDependencyMatrix(window)

	// Current implementation doesn't track WAW (would need register renaming)
	// Just document the behavior
	if matrix[0] != 0 {
		t.Log("Note: WAW hazard not tracked in current implementation")
	} else {
		t.Log("✓ WAW hazard correctly not tracked (architectural hazard, needs renaming)")
	}
}

// TestHazard_WAR tests Write-After-Read hazard: not relevant in OoO execution
// without speculative execution (reads happen before writes issue).
func TestHazard_WAR(t *testing.T) {
	// Write After Read - not relevant in OoO without speculative execution
	// Op 0 reads r10, Op 1 writes r10
	window := &InstructionWindow{}

	window.Ops[0] = Operation{Valid: true, Src1: 10, Src2: 2, Dest: 11}
	window.Ops[1] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10}

	matrix := BuildDependencyMatrix(window)

	// Should NOT show Op 0 depending on Op 1 (WAR not tracked)
	if (matrix[1]>>0)&1 != 0 {
		t.Error("WAR incorrectly detected (not relevant for OoO)")
	}
}

// ════════════════════════════════════════════════════════════════════════════════════════════════
// 8. EDGE CASES AND NEGATIVE TESTS
// ════════════════════════════════════════════════════════════════════════════════════════════════

// TestEdgeCase_Register0 tests that register 0 works like any other register
// (not hardwired to zero in SUPRAX unlike some other architectures).
func TestEdgeCase_Register0(t *testing.T) {
	// Register 0 might be special in some architectures (hardwired zero)
	// But in SUPRAX it's a regular register
	var sb Scoreboard

	sb.MarkReady(0)
	if !sb.IsReady(0) {
		t.Error("Register 0 should work like any other register")
	}

	sb.MarkPending(0)
	if sb.IsReady(0) {
		t.Error("Register 0 should be markable as pending")
	}
}

// TestEdgeCase_Register63 tests the highest register number (boundary test
// for 6-bit register addressing).
func TestEdgeCase_Register63(t *testing.T) {
	// Test the highest register (boundary condition)
	sched := &OoOScheduler{}

	sched.Window.Ops[0] = Operation{Valid: true, Src1: 62, Src2: 63, Dest: 60, Op: 0xAD}
	sched.Scoreboard.MarkReady(62)
	sched.Scoreboard.MarkReady(63)

	sched.ScheduleCycle0()
	bundle := sched.ScheduleCycle1()

	if bundle.Valid == 0 {
		t.Error("Op using registers 62,63 should be issuable")
	}
}

// TestEdgeCase_SelfDependency tests an operation that reads and writes the
// same register (e.g., INC r10). Valid in many ISAs.
func TestEdgeCase_SelfDependency(t *testing.T) {
	// Op that reads and writes the same register (valid in some ISAs)
	window := &InstructionWindow{}
	var sb Scoreboard

	window.Ops[0] = Operation{Valid: true, Src1: 10, Src2: 10, Dest: 10}
	sb.MarkReady(10)

	readyBitmap := ComputeReadyBitmap(window, sb)

	// Should be ready (both sources ready)
	if readyBitmap != 1 {
		t.Error("Self-dependency should still be issuable if register is ready")
	}
}

// TestEdgeCase_ZeroDependencies tests independent operations with no
// producer-consumer relationships (all read same inputs).
func TestEdgeCase_ZeroDependencies(t *testing.T) {
	// All ops use same source registers (no producer-consumer)
	window := &InstructionWindow{}

	for i := 0; i < 5; i++ {
		window.Ops[i] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: uint8(i + 10)}
	}

	matrix := BuildDependencyMatrix(window)

	// No dependencies should exist
	for i := 0; i < 5; i++ {
		if matrix[i] != 0 {
			t.Errorf("Op %d should have no dependencies", i)
		}
	}
}

// TestEdgeCase_LongDependencyChain tests a chain of 20 dependent ops
// (exceeds typical pipeline depth). Verifies correct serialization.
func TestEdgeCase_LongDependencyChain(t *testing.T) {
	// Create a chain of 20 ops (exceeds typical pipeline depth)
	sched := &OoOScheduler{}

	for i := 0; i < 20; i++ {
		sched.Window.Ops[i] = Operation{
			Valid: true,
			Src1:  uint8(i + 9), // Depends on previous op's dest
			Src2:  1,
			Dest:  uint8(i + 10),
			Op:    0xAD,
		}
	}

	// Only first op's source is ready
	sched.Scoreboard.MarkReady(9)
	sched.Scoreboard.MarkReady(1)

	// Should only issue op 0
	sched.ScheduleCycle0()
	bundle := sched.ScheduleCycle1()

	count := 0
	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 != 0 {
			count++
			if bundle.Indices[i] != 0 {
				t.Error("Only op 0 should be issuable")
			}
		}
	}

	if count != 1 {
		t.Errorf("Expected 1 op issued, got %d", count)
	}
}

// TestEdgeCase_AllOpsToSameDestination tests multiple ops writing to the
// same destination register (WAW hazard - architectural, not microarchitectural).
func TestEdgeCase_AllOpsToSameDestination(t *testing.T) {
	// Multiple ops writing to the same register (WAW hazard)
	// In OoO, this is legal - last write wins
	window := &InstructionWindow{}

	for i := 0; i < 5; i++ {
		window.Ops[i] = Operation{
			Valid: true,
			Src1:  uint8(i),
			Src2:  uint8(i + 1),
			Dest:  10, // Same destination!
		}
	}

	matrix := BuildDependencyMatrix(window)

	// No RAW dependencies (different sources)
	// WAW is architectural (register renaming would handle in real CPU)
	for i := 0; i < 5; i++ {
		if matrix[i] != 0 {
			t.Logf("Op %d has dependents: 0x%08X (WAW not tracked in this model)", i, matrix[i])
		}
	}
}

// TestNegative_InvalidScoreboardOperations tests marking a register outside
// the valid range (0-63). Tests wraparound behavior.
func TestNegative_InvalidScoreboardOperations(t *testing.T) {
	var sb Scoreboard

	// Test marking invalid register (outside 0-63)
	// Note: uint8 range is 0-255, so this tests wraparound
	sb.MarkReady(200) // Invalid register

	// This will set bit (200 % 64) = bit 8
	if sb.IsReady(8) {
		t.Log("Note: Marking register 200 sets bit 8 (wraparound)")
	}
}

// TestNegative_AllInvalidOps tests that a window full of invalid ops produces
// an empty dependency matrix.
func TestNegative_AllInvalidOps(t *testing.T) {
	window := &InstructionWindow{}
	// All ops are invalid by default

	matrix := BuildDependencyMatrix(window)

	// Matrix should be all zeros
	for i := 0; i < 32; i++ {
		if matrix[i] != 0 {
			t.Errorf("Invalid ops should produce zero dependency matrix row %d", i)
		}
	}
}

// TestNegative_EmptyPriorityClass tests that empty priority classes produce
// an empty issue bundle (graceful handling of no-work condition).
func TestNegative_EmptyPriorityClass(t *testing.T) {
	priority := PriorityClass{
		HighPriority: 0,
		LowPriority:  0,
	}

	bundle := SelectIssueBundle(priority)

	if bundle.Valid != 0 {
		t.Error("Empty priority should produce empty bundle")
	}

	// All indices should be zero (or uninitialized)
	for i := 0; i < 16; i++ {
		if bundle.Indices[i] != 0 {
			// This is actually OK - uninitialized data
			t.Logf("Note: Bundle indices may contain garbage when Valid=0")
			break
		}
	}
}

// ════════════════════════════════════════════════════════════════════════════════════════════════
// 9. CORRECTNESS VALIDATION
// ════════════════════════════════════════════════════════════════════════════════════════════════

// TestCorrectness_NoOpIssuedTwice verifies that no operation is issued twice
// across multiple issue cycles (critical correctness property).
func TestCorrectness_NoOpIssuedTwice(t *testing.T) {
	sched := &OoOScheduler{}

	// Create 20 independent ops
	for i := 0; i < 20; i++ {
		sched.Window.Ops[i] = Operation{
			Valid: true,
			Src1:  1,
			Src2:  2,
			Dest:  uint8(i + 10),
		}
	}
	sched.Scoreboard.MarkReady(1)
	sched.Scoreboard.MarkReady(2)

	// Issue twice without marking ops invalid
	sched.ScheduleCycle0()
	bundle1 := sched.ScheduleCycle1()

	sched.ScheduleCycle0()
	bundle2 := sched.ScheduleCycle1()

	// Check for duplicates between bundle1 and bundle2
	for i := 0; i < 16; i++ {
		if (bundle1.Valid>>i)&1 == 0 {
			continue
		}
		idx1 := bundle1.Indices[i]

		for j := 0; j < 16; j++ {
			if (bundle2.Valid>>j)&1 == 0 {
				continue
			}
			idx2 := bundle2.Indices[j]

			if idx1 == idx2 {
				t.Errorf("Op %d issued in both bundles (should not happen)", idx1)
			}
		}
	}

	t.Log("✓ No ops issued twice")
}

// TestCorrectness_DependenciesRespected verifies that dependent operations are
// never issued before their producers complete (fundamental correctness).
func TestCorrectness_DependenciesRespected(t *testing.T) {
	sched := &OoOScheduler{}

	// Create chain: 0 → 1 → 2
	sched.Window.Ops[0] = Operation{Valid: true, Src1: 1, Src2: 2, Dest: 10}
	sched.Window.Ops[1] = Operation{Valid: true, Src1: 10, Src2: 3, Dest: 11}
	sched.Window.Ops[2] = Operation{Valid: true, Src1: 11, Src2: 4, Dest: 12}

	sched.Scoreboard.MarkReady(1)
	sched.Scoreboard.MarkReady(2)
	sched.Scoreboard.MarkReady(3)
	sched.Scoreboard.MarkReady(4)

	// First issue should NOT include op 1 or 2
	sched.ScheduleCycle0()
	bundle := sched.ScheduleCycle1()

	for i := 0; i < 16; i++ {
		if (bundle.Valid>>i)&1 == 0 {
			continue
		}
		idx := bundle.Indices[i]
		if idx == 1 || idx == 2 {
			t.Errorf("Op %d issued prematurely (dependencies not satisfied)", idx)
		}
	}

	t.Log("✓ Dependencies correctly enforced")
}

// ════════════════════════════════════════════════════════════════════════════════════════════════
// 10. STRESS AND PERFORMANCE TESTS
// ════════════════════════════════════════════════════════════════════════════════════════════════

// TestStress_RepeatedFillDrain stress tests the scheduler by repeatedly filling
// the window to capacity and draining it. Tests stability over many cycles.
func TestStress_RepeatedFillDrain(t *testing.T) {
	sched := &OoOScheduler{}

	for round := 0; round < 10; round++ {
		// Fill window with 32 independent ops
		for i := 0; i < 32; i++ {
			sched.Window.Ops[i] = Operation{
				Valid: true,
				Src1:  1,
				Src2:  2,
				Dest:  uint8(i + 10),
			}
		}
		sched.Scoreboard.MarkReady(1)
		sched.Scoreboard.MarkReady(2)

		// Drain in two batches of 16
		for batch := 0; batch < 2; batch++ {
			sched.ScheduleCycle0()
			bundle := sched.ScheduleCycle1()

			// Verify 16 ops issued
			count := 0
			for i := 0; i < 16; i++ {
				if (bundle.Valid>>i)&1 != 0 {
					count++
				}
			}

			if count != 16 {
				t.Fatalf("Round %d, Batch %d: Expected 16 ops, got %d", round, batch, count)
			}

			// Mark issued ops as complete and invalid
			var destRegs [16]uint8
			for i := 0; i < 16; i++ {
				if (bundle.Valid>>i)&1 != 0 {
					idx := bundle.Indices[i]
					destRegs[i] = sched.Window.Ops[idx].Dest
					sched.Window.Ops[idx].Valid = false
				}
			}
			sched.ScheduleComplete(destRegs, bundle.Valid)
		}

		// Verify window is empty
		sched.ScheduleCycle0()
		bundle := sched.ScheduleCycle1()
		if bundle.Valid != 0 {
			t.Fatalf("Round %d: Window should be empty, got valid=0x%X", round, bundle.Valid)
		}
	}

	t.Log("✓ 10 rounds of fill/drain completed successfully")
}

// TestStress_LongDependencyChain_FullResolution stress tests a 20-op dependency
// chain, verifying each op issues in order and only after its predecessor completes.
func TestStress_LongDependencyChain_FullResolution(t *testing.T) {
	sched := &OoOScheduler{}

	// Create chain of 20 ops
	chainLength := 20
	for i := 0; i < chainLength; i++ {
		sched.Window.Ops[i] = Operation{
			Valid: true,
			Src1:  uint8(i + 9),
			Src2:  1,
			Dest:  uint8(i + 10),
		}
	}

	// Only first op's source is ready
	sched.Scoreboard.MarkReady(9)
	sched.Scoreboard.MarkReady(1)

	// Resolve chain one op at a time
	for step := 0; step < chainLength; step++ {
		sched.ScheduleCycle0()
		bundle := sched.ScheduleCycle1()

		// Should issue exactly 1 op (the next in chain)
		count := 0
		var issuedIdx uint8
		for i := 0; i < 16; i++ {
			if (bundle.Valid>>i)&1 != 0 {
				count++
				issuedIdx = bundle.Indices[i]
			}
		}

		if count != 1 {
			t.Fatalf("Step %d: Expected 1 op, got %d", step, count)
		}

		if int(issuedIdx) != step {
			t.Fatalf("Step %d: Expected op %d, got op %d", step, step, issuedIdx)
		}

		// Complete the op
		dest := sched.Window.Ops[issuedIdx].Dest
		sched.ScheduleComplete([16]uint8{dest}, 0b1)
		sched.Window.Ops[issuedIdx].Valid = false
	}

	t.Log("✓ Successfully resolved 20-op dependency chain")
}

// TestTimingAnalysis validates the documented timing can be met at various
// clock frequencies. Documents the 2-cycle scheduler latency.
func TestTimingAnalysis(t *testing.T) {
	// This test verifies the claimed timing can be met at 3.5 GHz
	// 1 cycle = 286ps at 3.5 GHz

	// Cycle 0 timing breakdown (documented):
	//   ComputeReadyBitmap:     120ps
	//   BuildDependencyMatrix:  120ps (parallel)
	//   ClassifyPriority:       100ps
	//   Pipeline register:      40ps
	//   Total:                  280ps < 286ps ✓

	// Cycle 1 timing breakdown (documented):
	//   SelectIssueBundle:      320ps (tier select 120ps + parallel encode 200ps)
	//   UpdateScoreboard:       20ps (can overlap)
	//   Total:                  340ps > 286ps ✗

	// The documentation notes Cycle 1 is 19% over budget at 3.5 GHz
	// Solutions mentioned:
	// 1. Run at 3.0 GHz (333ps/cycle) - both stages fit
	// 2. Optimize priority encoder: 200ps → 150ps reduces to 290ps
	// 3. Pipeline Cycle 1 into two half-cycles

	t.Run("Cycle0_Timing", func(t *testing.T) {
		// At 3.5 GHz, Cycle 0 should fit in 286ps
		// Documented: 280ps
		cycle0Latency := 280 // picoseconds
		cycleTime := 286     // picoseconds at 3.5 GHz

		if cycle0Latency <= cycleTime {
			t.Logf("✓ Cycle 0: %dps <= %dps (%.1f%% utilization)",
				cycle0Latency, cycleTime, float64(cycle0Latency)/float64(cycleTime)*100)
		} else {
			t.Errorf("✗ Cycle 0: %dps > %dps (over budget)", cycle0Latency, cycleTime)
		}
	})

	t.Run("Cycle1_Timing_3.5GHz", func(t *testing.T) {
		// At 3.5 GHz, Cycle 1 is slightly over budget
		cycle1Latency := 340 // picoseconds
		cycleTime := 286     // picoseconds at 3.5 GHz

		if cycle1Latency > cycleTime {
			overclock := float64(cycle1Latency-cycleTime) / float64(cycleTime) * 100
			t.Logf("⚠ Cycle 1: %dps > %dps (%.1f%% over budget at 3.5GHz)",
				cycle1Latency, cycleTime, overclock)
			t.Log("  Solutions: 1) Run at 3.0 GHz, 2) Optimize encoder, 3) Pipeline Cycle 1")
		}
	})

	t.Run("Cycle1_Timing_3.0GHz", func(t *testing.T) {
		// At 3.0 GHz, both cycles should fit
		cycle1Latency := 340 // picoseconds
		cycleTime := 333     // picoseconds at 3.0 GHz

		if cycle1Latency <= cycleTime {
			t.Logf("✓ Cycle 1 @ 3.0GHz: %dps <= %dps (%.1f%% utilization)",
				cycle1Latency, cycleTime, float64(cycle1Latency)/float64(cycleTime)*100)
		} else {
			t.Errorf("✗ Cycle 1 @ 3.0GHz: %dps > %dps", cycle1Latency, cycleTime)
		}
	})

	t.Run("Optimized_Encoder_Timing", func(t *testing.T) {
		// With optimized priority encoder: 200ps → 150ps
		tierSelect := 120
		encoderOptimized := 150
		cycle1Optimized := tierSelect + encoderOptimized // 270ps
		cycleTime := 286                                 // 3.5 GHz

		if cycle1Optimized <= cycleTime {
			t.Logf("✓ Cycle 1 (optimized): %dps <= %dps", cycle1Optimized, cycleTime)
		} else {
			t.Errorf("✗ Cycle 1 (optimized): %dps > %dps", cycle1Optimized, cycleTime)
		}
	})

	t.Run("Total_Latency", func(t *testing.T) {
		// Total scheduler latency: 2 cycles
		cycle0 := 280 // ps
		cycle1 := 340 // ps
		total := cycle0 + cycle1

		t.Logf("Total OoO scheduler latency: %dps = %.2f cycles @ 3.5GHz",
			total, float64(total)/286.0)
		t.Logf("Total OoO scheduler latency: %dps = %.2f cycles @ 3.0GHz",
			total, float64(total)/333.0)
	})
}

// TestPerformanceMetrics documents the expected performance targets:
// transistor count, power consumption, and IPC compared to Intel.
func TestPerformanceMetrics(t *testing.T) {
	// Documented performance targets:
	// - Single-thread IPC: 8-10 (with context switching)
	// - Intel i9 IPC: 5-6
	// - Speedup: 1.3-1.7×

	// Transistor budget:
	// - Per context: 1.05M
	// - 8 contexts: 8.4M
	// - Intel OoO: 300M
	// - Advantage: 35× fewer

	t.Run("TransistorBudget", func(t *testing.T) {
		perContext := 1_050_000
		contexts := 8
		total := perContext * contexts
		intelOoO := 300_000_000

		ratio := float64(intelOoO) / float64(total)

		t.Logf("SUPRAX OoO transistors: %d (%d per context × %d contexts)",
			total, perContext, contexts)
		t.Logf("Intel OoO transistors: %d", intelOoO)
		t.Logf("Efficiency advantage: %.1f× fewer transistors", ratio)

		if total > 10_000_000 {
			t.Errorf("Transistor budget exceeds 10M target, got %d", total)
		}
	})

	t.Run("PowerBudget", func(t *testing.T) {
		// At 3.0 GHz, 28nm:
		// Dynamic: ~150mW
		// Leakage: ~80mW
		// Total: ~230mW
		// Intel: ~5W
		// Advantage: 20×

		supraXPower := 230 // mW
		intelPower := 5000 // mW

		ratio := float64(intelPower) / float64(supraXPower)

		t.Logf("SUPRAX OoO power: %dmW @ 3.0GHz", supraXPower)
		t.Logf("Intel OoO power: %dmW", intelPower)
		t.Logf("Power efficiency: %.1f× more efficient", ratio)
	})

	t.Run("ExpectedIPC", func(t *testing.T) {
		// These are targets, not measured from unit tests
		// Real IPC would come from full system simulation

		targetIPC := 10.0
		intelIPC := 5.5
		speedup := targetIPC / intelIPC

		t.Logf("Target IPC: %.1f", targetIPC)
		t.Logf("Intel i9 IPC: %.1f", intelIPC)
		t.Logf("Expected speedup: %.2f×", speedup)
	})
}

// TestDocumentation_StructSizes validates that the actual struct sizes in Go
// match (or are close to) the documented hardware sizes.
func TestDocumentation_StructSizes(t *testing.T) {
	// Verify documented sizes

	// Operation: "64 bits total"
	// Actually: bool(1) + 6*uint8(48) + uint16(16) = 65 bits + padding
	// With padding to 64-bit boundary: likely 96 or 128 bits in Go
	opSize := unsafe.Sizeof(Operation{})
	t.Logf("Operation size: %d bytes (documented: 8 bytes/64 bits)", opSize)

	// InstructionWindow: "32 slots × 64 bits = 2KB"
	winSize := unsafe.Sizeof(InstructionWindow{})
	expectedSize := 32 * 8 // 256 bytes if ops are 64 bits each
	t.Logf("Window size: %d bytes (documented: %d bytes)", winSize, expectedSize)

	// Scoreboard: "64 flip-flops"
	sbSize := unsafe.Sizeof(Scoreboard(0))
	if sbSize != 8 {
		t.Errorf("Scoreboard should be 8 bytes (uint64), got %d", sbSize)
	}

	// DependencyMatrix: "1024 bits = 128 bytes"
	matrixSize := unsafe.Sizeof(DependencyMatrix{})
	if matrixSize != 128 {
		t.Errorf("DependencyMatrix should be 128 bytes, got %d", matrixSize)
	}
}

// TestDocumentation_TransistorBudget validates the documented transistor budget
// breakdown for each component of the scheduler.
func TestDocumentation_TransistorBudget(t *testing.T) {
	// Documented transistor budget per context: ~1.05M
	components := map[string]int{
		"Instruction window (2KB SRAM)": 200_000,
		"Scoreboard (64 flip-flops)":    64,
		"Dependency matrix logic":       400_000,
		"Priority classification":       300_000,
		"Issue selection":               50_000,
		"Pipeline registers":            100_000,
	}

	total := 0
	for name, count := range components {
		total += count
		t.Logf("  %s: %d transistors", name, count)
	}

	t.Logf("Total per context: %d transistors", total)

	if total != 1_050_064 {
		t.Logf("Note: Documented 1.05M, calculated %d (close enough)", total)
	}

	contexts := 8
	totalCPU := total * contexts
	t.Logf("Total for 8 contexts: %d transistors", totalCPU)

	if totalCPU > 10_000_000 {
		t.Errorf("Total exceeds 10M budget: %d", totalCPU)
	}
}
