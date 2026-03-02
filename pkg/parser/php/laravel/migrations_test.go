package laravel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationAnalyzer_Analyze(t *testing.T) {
	content := `<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up()
    {
        Schema::create('users', function (Blueprint $table) {
            $table->id();
            $table->string('name');
            $table->timestamps();
        });
    }

    public function down()
    {
        Schema::dropIfExists('users');
    }
};
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "2024_01_01_000000_create_users_table.php")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	analyzer := NewMigrationAnalyzer()
	migrations, err := analyzer.Analyze([]string{filePath})
	if err != nil {
		t.Fatalf("Failed to analyze migrations: %v", err)
	}

	if len(migrations) != 1 {
		t.Fatalf("Expected 1 migration, got %d", len(migrations))
	}

	mig := migrations[0]
	if mig.ClassName != "AnonymousMigration" {
		t.Errorf("Expected AnonymousMigration, got %s", mig.ClassName)
	}
	if mig.Table != "users" {
		t.Errorf("Expected table 'users', got %s", mig.Table)
	}
	if len(mig.Operations) == 0 || mig.Operations[0] != "create" {
		t.Errorf("Expected first operation to be 'create', got %v", mig.Operations)
	}
}
