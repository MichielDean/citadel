package main

import (
	"strings"
	"testing"
)

// TestArchPixelMap_Has14RowsOf28Cols verifies the pixel map has exactly 14 rows of 28 columns.
//
// Given: the static archPixelMap constant
// When:  its dimensions are inspected
// Then:  it has archPillarH rows and archPillarW columns each
func TestArchPixelMap_Has14RowsOf28Cols(t *testing.T) {
	if len(archPixelMap) != archPillarH {
		t.Errorf("archPixelMap has %d rows, want %d", len(archPixelMap), archPillarH)
	}
	for r, row := range archPixelMap {
		if len(row) != archPillarW {
			t.Errorf("archPixelMap[%d] has %d cols, want %d", r, len(row), archPillarW)
		}
	}
}

// TestArchPixelMap_Rows0to4_AreBlank verifies rows 0–4 are entirely blank.
// These rows represent the space above the arch crown.
//
// Given: archPixelMap rows 0–4
// When:  each cell is inspected
// Then:  every cell is pxBlank
func TestArchPixelMap_Rows0to4_AreBlank(t *testing.T) {
	for r := 0; r < 5; r++ {
		for c, px := range archPixelMap[r] {
			if px != pxBlank {
				t.Errorf("archPixelMap[%d][%d] = %q, want pxBlank (space above crown)", r, c, px)
			}
		}
	}
}

// TestArchPixelMap_Row5_IsFullWidthCrown verifies row 5 is full-width fill.
// Row 5 represents the arch crown / road surface.
//
// Given: archPixelMap row 5
// When:  each cell is inspected
// Then:  all 28 cells are pxFill ('▒')
func TestArchPixelMap_Row5_IsFullWidthCrown(t *testing.T) {
	for c, px := range archPixelMap[5] {
		if px != pxFill {
			t.Errorf("archPixelMap[5][%d] = %q, want pxFill '▒' (arch crown)", c, px)
		}
	}
}

// TestArchPixelMap_Row6_ArchOpeningShape verifies row 6 encodes the arch opening.
// Expected layout: 6 blank + 1 edge + 16 fill + 5 blank = 28 cols.
//
// Given: archPixelMap row 6
// When:  each cell group is inspected
// Then:  columns 0–5 blank, column 6 edge, columns 7–22 fill, columns 23–27 blank
func TestArchPixelMap_Row6_ArchOpeningShape(t *testing.T) {
	row := archPixelMap[6]
	for c := 0; c < 6; c++ {
		if row[c] != pxBlank {
			t.Errorf("archPixelMap[6][%d] = %q, want pxBlank (leading indent)", c, row[c])
		}
	}
	if row[6] != pxEdge {
		t.Errorf("archPixelMap[6][6] = %q, want pxEdge '░' (arch edge)", row[6])
	}
	for c := 7; c <= 22; c++ {
		if row[c] != pxFill {
			t.Errorf("archPixelMap[6][%d] = %q, want pxFill '▒' (arch fill)", c, row[c])
		}
	}
	for c := 23; c <= 27; c++ {
		if row[c] != pxBlank {
			t.Errorf("archPixelMap[6][%d] = %q, want pxBlank (trailing)", c, row[c])
		}
	}
}

// TestArchPixelMap_Row7_ArchNarrowingShape verifies row 7 encodes a narrower arch opening.
// Expected layout: 9 blank + 1 edge + 9 fill + 9 blank = 28 cols.
//
// Given: archPixelMap row 7
// When:  each cell group is inspected
// Then:  columns 0–8 blank, column 9 edge, columns 10–18 fill, columns 19–27 blank
func TestArchPixelMap_Row7_ArchNarrowingShape(t *testing.T) {
	row := archPixelMap[7]
	for c := 0; c < 9; c++ {
		if row[c] != pxBlank {
			t.Errorf("archPixelMap[7][%d] = %q, want pxBlank", c, row[c])
		}
	}
	if row[9] != pxEdge {
		t.Errorf("archPixelMap[7][9] = %q, want pxEdge '░'", row[9])
	}
	for c := 10; c <= 18; c++ {
		if row[c] != pxFill {
			t.Errorf("archPixelMap[7][%d] = %q, want pxFill '▒'", c, row[c])
		}
	}
	for c := 19; c <= 27; c++ {
		if row[c] != pxBlank {
			t.Errorf("archPixelMap[7][%d] = %q, want pxBlank", c, row[c])
		}
	}
}

// TestArchPixelMap_Row8_ArchNarrowingShape verifies row 8 encodes the narrowest arch section.
// Expected layout: 10 blank + 1 edge + 7 fill + 10 blank = 28 cols.
//
// Given: archPixelMap row 8
// When:  each cell group is inspected
// Then:  columns 0–9 blank, column 10 edge, columns 11–17 fill, columns 18–27 blank
func TestArchPixelMap_Row8_ArchNarrowingShape(t *testing.T) {
	row := archPixelMap[8]
	for c := 0; c < 10; c++ {
		if row[c] != pxBlank {
			t.Errorf("archPixelMap[8][%d] = %q, want pxBlank", c, row[c])
		}
	}
	if row[10] != pxEdge {
		t.Errorf("archPixelMap[8][10] = %q, want pxEdge '░'", row[10])
	}
	for c := 11; c <= 17; c++ {
		if row[c] != pxFill {
			t.Errorf("archPixelMap[8][%d] = %q, want pxFill '▒'", c, row[c])
		}
	}
	for c := 18; c <= 27; c++ {
		if row[c] != pxBlank {
			t.Errorf("archPixelMap[8][%d] = %q, want pxBlank", c, row[c])
		}
	}
}

// TestArchPixelMap_Rows9to13_PierBodyShape verifies all pier body rows have the same shape.
// Expected layout: 12 blank + 1 edge + 4 fill + 11 blank = 28 cols.
//
// Given: archPixelMap rows 9–13
// When:  each row's cells are inspected
// Then:  columns 0–11 blank, column 12 edge, columns 13–16 fill, columns 17–27 blank
func TestArchPixelMap_Rows9to13_PierBodyShape(t *testing.T) {
	for r := 9; r <= 13; r++ {
		row := archPixelMap[r]
		for c := 0; c < 12; c++ {
			if row[c] != pxBlank {
				t.Errorf("archPixelMap[%d][%d] = %q, want pxBlank (pier indent)", r, c, row[c])
			}
		}
		if row[12] != pxEdge {
			t.Errorf("archPixelMap[%d][12] = %q, want pxEdge '░' (pier edge)", r, row[12])
		}
		for c := 13; c <= 16; c++ {
			if row[c] != pxFill {
				t.Errorf("archPixelMap[%d][%d] = %q, want pxFill '▒' (pier fill)", r, c, row[c])
			}
		}
		for c := 17; c <= 27; c++ {
			if row[c] != pxBlank {
				t.Errorf("archPixelMap[%d][%d] = %q, want pxBlank (pier trailing)", r, c, row[c])
			}
		}
	}
}

// TestRenderArchPillarRow_Row5_ContainsFillChar verifies that rendering row 5 produces output
// containing fill characters ('▒').
//
// Given: row 5 of archPixelMap (full-width crown)
// When:  renderArchPillarRow is called with active=false
// Then:  the output contains '▒'
func TestRenderArchPillarRow_Row5_ContainsFillChar(t *testing.T) {
	got := renderArchPillarRow(5, false)
	if !strings.Contains(got, "▒") {
		t.Errorf("renderArchPillarRow(5, false): expected '▒' in output, got %q", got)
	}
}

// TestRenderArchPillarRow_Row5_Active_ContainsFillChar verifies that rendering row 5 with
// active=true also produces fill characters.
//
// Given: row 5 of archPixelMap
// When:  renderArchPillarRow is called with active=true
// Then:  the output contains '▒'
func TestRenderArchPillarRow_Row5_Active_ContainsFillChar(t *testing.T) {
	got := renderArchPillarRow(5, true)
	if !strings.Contains(got, "▒") {
		t.Errorf("renderArchPillarRow(5, true): expected '▒' in output, got %q", got)
	}
}

// TestRenderArchPillarRow_Row6_ContainsEdgeChar verifies that rendering row 6 produces output
// containing an edge character ('░').
//
// Given: row 6 of archPixelMap (arch opening with one edge pixel at col 6)
// When:  renderArchPillarRow is called
// Then:  the output contains '░'
func TestRenderArchPillarRow_Row6_ContainsEdgeChar(t *testing.T) {
	got := renderArchPillarRow(6, false)
	if !strings.Contains(got, "░") {
		t.Errorf("renderArchPillarRow(6, false): expected '░' in output, got %q", got)
	}
}

// TestRenderDroughtPillarRow_Row5_ContainsFillChar verifies that the drought renderer
// produces fill characters for row 5.
//
// Given: row 5 of archPixelMap (full-width crown)
// When:  renderDroughtPillarRow is called
// Then:  the output contains '▒'
func TestRenderDroughtPillarRow_Row5_ContainsFillChar(t *testing.T) {
	got := renderDroughtPillarRow(5)
	if !strings.Contains(got, "▒") {
		t.Errorf("renderDroughtPillarRow(5): expected '▒' in output, got %q", got)
	}
}

// TestRenderDroughtPillarRow_Rows0to4_ProduceNoVisibleChars verifies that drought rendering
// of blank rows produces only whitespace.
//
// Given: rows 0–4 of archPixelMap (blank above arch crown)
// When:  renderDroughtPillarRow is called for each
// Then:  the output contains no non-space characters
func TestRenderDroughtPillarRow_Rows0to4_ProduceNoVisibleChars(t *testing.T) {
	for r := 0; r < 5; r++ {
		got := renderDroughtPillarRow(r)
		if strings.TrimSpace(got) != "" {
			t.Errorf("renderDroughtPillarRow(%d): expected blank output, got %q", r, got)
		}
	}
}

// TestRenderArchPillarRow_Rows0to4_ProduceNoVisibleChars verifies that rendering blank
// rows above the crown produces only whitespace (with background color applied).
//
// Given: rows 0–4 of archPixelMap
// When:  renderArchPillarRow is called for each
// Then:  the output contains no non-space characters
func TestRenderArchPillarRow_Rows0to4_ProduceNoVisibleChars(t *testing.T) {
	for r := 0; r < 5; r++ {
		got := renderArchPillarRow(r, false)
		if strings.TrimSpace(got) != "" {
			t.Errorf("renderArchPillarRow(%d, false): expected blank output, got %q", r, got)
		}
	}
}
