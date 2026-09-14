package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const knowledgeFixture = `# База знаний Dzala

## Кэмпы

### Тбилиси, Грузия

#### Краткое описание

Кэмп в Тбилиси с тренировками и гольфом.

#### Даты

18.10–25.10. **Год не указан.**

### Кейптаун, ЮАР

#### Краткое описание

Кэмп в Кейптауне.
`

func writeKnowledgeFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dzala.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadKnowledgeReadsFile(t *testing.T) {
	knowledge, err := loadKnowledge(writeKnowledgeFixture(t, knowledgeFixture))
	if err != nil {
		t.Fatalf("loadKnowledge() error = %v", err)
	}
	if !strings.Contains(knowledge, "Кэмп в Тбилиси") {
		t.Error("loadKnowledge() did not return the file content")
	}
}

func TestLoadKnowledgeRejectsMissingFile(t *testing.T) {
	if _, err := loadKnowledge(filepath.Join(t.TempDir(), "missing.md")); err == nil {
		t.Fatal("loadKnowledge() error = nil, want an error for a missing file")
	}
}

func TestLoadKnowledgeRejectsEmptyFile(t *testing.T) {
	if _, err := loadKnowledge(writeKnowledgeFixture(t, "\n   \n")); err == nil {
		t.Fatal("loadKnowledge() error = nil, want an error for an empty file")
	}
}

func TestCampSummaryUsesKnowledgeBase(t *testing.T) {
	selected, ok := campByID("tbilisi")
	if !ok {
		t.Fatal("campByID(\"tbilisi\") not found")
	}

	summary, ok := campSummary(knowledgeFixture, selected)
	if !ok {
		t.Fatal("campSummary() ok = false, want true")
	}
	for _, want := range []string{"Тбилиси, Грузия", "Кэмп в Тбилиси с тренировками и гольфом.", "Даты: 18.10–25.10"} {
		if !strings.Contains(summary, want) {
			t.Errorf("campSummary() = %q, want it to contain %q", summary, want)
		}
	}
	if strings.Contains(summary, "**") {
		t.Error("campSummary() must not contain Markdown emphasis")
	}
	if strings.Contains(summary, "Кейптаун") {
		t.Error("campSummary() must not leak another camp section")
	}
}

func TestCampDetailsUsesFullKnowledgeSection(t *testing.T) {
	selected, _ := campByID("tbilisi")
	details, ok := campDetails(knowledgeFixture, selected)
	if !ok {
		t.Fatal("campDetails() ok = false, want true")
	}
	for _, want := range []string{"Краткое описание", "Кэмп в Тбилиси", "Даты", "18.10–25.10"} {
		if !strings.Contains(details, want) {
			t.Errorf("campDetails() = %q, want it to contain %q", details, want)
		}
	}
	if strings.Contains(details, "Кейптаун") {
		t.Error("campDetails() must not leak another camp section")
	}
	if strings.Contains(details, "####") || strings.Contains(details, "| ---") {
		t.Error("campDetails() must be readable without Telegram Markdown parsing")
	}
}

func TestCampSummaryMissingSection(t *testing.T) {
	if _, ok := campSummary("# База знаний\n", camp{id: "tbilisi", heading: "Тбилиси"}); ok {
		t.Error("campSummary() ok = true, want false when the section is missing")
	}
}

func TestCampTitleFallsBackToHeading(t *testing.T) {
	if got := campTitle(knowledgeFixture, "tbilisi"); got != "Тбилиси, Грузия" {
		t.Errorf("campTitle() = %q, want %q", got, "Тбилиси, Грузия")
	}
	if got := campTitle(knowledgeFixture, "unknown"); got != "" {
		t.Errorf("campTitle() = %q, want an empty string for an unknown camp", got)
	}
}

// TestRepositoryKnowledgeCoversEveryCamp guards the menu against knowledge base edits.
func TestRepositoryKnowledgeCoversEveryCamp(t *testing.T) {
	knowledge, err := loadKnowledge(filepath.Join("..", "..", defaultKnowledgeFile))
	if err != nil {
		t.Fatalf("loadKnowledge() error = %v", err)
	}

	for _, item := range camps {
		if _, ok := campSummary(knowledge, item); !ok {
			t.Errorf("campSummary() ok = false for camp %q", item.id)
		}
	}
}
