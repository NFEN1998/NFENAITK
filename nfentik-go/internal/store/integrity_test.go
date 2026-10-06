package store

import "testing"

func TestInspectHealthyDatabase(t *testing.T) {
	s := testStore(t)
	report, err := s.Inspect()
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if report.Empty {
		t.Fatalf("expected a populated database, got Empty")
	}
	if len(report.MissingTables) != 0 {
		t.Fatalf("unexpected missing tables: %v", report.MissingTables)
	}
	if len(report.MissingColumns) != 0 {
		t.Fatalf("unexpected missing columns: %v", report.MissingColumns)
	}
	if !report.HasVersionTable {
		t.Fatalf("expected SchemaMigrations to exist")
	}
	if report.CurrentVersion != report.TargetVersion {
		t.Fatalf("version %d != target %d", report.CurrentVersion, report.TargetVersion)
	}
	if !report.Healthy() {
		t.Fatalf("report should be healthy: %+v", report)
	}
}

func TestColumnRepairsCoverCoreColumns(t *testing.T) {
	// Every column listed in columnRepairs must belong to a known core table
	// and be part of that table's expected columns, so the repair list and the
	// inspection list can never drift apart.
	for _, fix := range columnRepairs {
		cols, ok := coreColumns[fix.Table]
		if !ok {
			t.Fatalf("columnRepair references unknown table %q", fix.Table)
		}
		found := false
		for _, c := range cols {
			if c == fix.Column {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("columnRepair %s.%s not in coreColumns", fix.Table, fix.Column)
		}
	}
}

func TestCoreTablesHaveColumns(t *testing.T) {
	for _, table := range coreTables {
		if len(coreColumns[table]) == 0 {
			t.Fatalf("core table %q has no expected columns", table)
		}
	}
}
