package postgres

import (
	"testing"
	"testing/fstest"
)

func TestMigrationSetRejectsMisnamedFiles(t *testing.T) {
	statement := &fstest.MapFile{Data: []byte("SELECT 1;")}
	cases := map[string]fstest.MapFS{
		"no migrations":  {},
		"starts at two":  {"0002_first.sql": statement},
		"gap":            {"0001_first.sql": statement, "0003_third.sql": statement},
		"duplicate":      {"0001_first.sql": statement, "0001_again.sql": statement},
		"short number":   {"1_first.sql": statement},
		"capital letter": {"0001_First.sql": statement},
		"directory":      {"0001_first.sql": statement, "0002_nested/file.sql": statement},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := (MigrationSet{HistoryTable: "test_schema_migrations", Files: files}).load(); err == nil {
				t.Fatal("misnamed migrations were accepted")
			}
		})
	}
	valid := fstest.MapFS{"0001_first.sql": statement, "0002_second_step.sql": statement}
	migrations, err := (MigrationSet{HistoryTable: "test_schema_migrations", Files: valid}).load()
	if err != nil || len(migrations) != 2 || migrations[1].version != 2 {
		t.Fatalf("load valid migrations = %+v, %v", migrations, err)
	}
}
