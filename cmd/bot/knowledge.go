package main

import (
	"fmt"
	"os"
	"strings"
)

const defaultKnowledgeFile = "knowledge/dzala.md"

// camp links a menu button to the matching section of the knowledge base.
// Camp facts live only in the knowledge file, never in the code.
type camp struct {
	id      string
	label   string
	heading string
}

var camps = []camp{
	{id: "tsinandali", label: "🇬🇪 Цинандали", heading: "Цинандали"},
	{id: "tbilisi", label: "🇬🇪 Тбилиси", heading: "Тбилиси"},
	{id: "cape_town", label: "🇿🇦 Кейптаун", heading: "Кейптаун"},
}

func campByID(id string) (camp, bool) {
	for _, item := range camps {
		if item.id == id {
			return item, true
		}
	}
	return camp{}, false
}

// loadKnowledge reads the knowledge base once at startup.
func loadKnowledge(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultKnowledgeFile
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read knowledge file %q: %w", path, err)
	}
	if strings.TrimSpace(string(content)) == "" {
		return "", fmt.Errorf("knowledge file %q is empty", path)
	}
	return string(content), nil
}

// campSection returns the title and the body of a "### <heading>" block.
func campSection(knowledge, heading string) (string, string, bool) {
	lines := strings.Split(knowledge, "\n")
	start := -1
	for index, line := range lines {
		if headingLevel(line) == 3 && strings.Contains(line, heading) {
			start = index
			break
		}
	}
	if start < 0 {
		return "", "", false
	}

	end := len(lines)
	for index := start + 1; index < len(lines); index++ {
		if level := headingLevel(lines[index]); level > 0 && level <= 3 {
			end = index
			break
		}
	}

	title := strings.TrimSpace(strings.TrimLeft(lines[start], "#"))
	body := strings.TrimSpace(strings.Join(lines[start+1:end], "\n"))
	if title == "" || body == "" {
		return "", "", false
	}
	return title, body, true
}

// subsection returns the body of a "#### <title>" block inside a camp section.
func subsection(section, title string) (string, bool) {
	lines := strings.Split(section, "\n")
	start := -1
	for index, line := range lines {
		if headingLevel(line) == 4 && strings.Contains(line, title) {
			start = index + 1
			break
		}
	}
	if start < 0 {
		return "", false
	}

	end := len(lines)
	for index := start; index < len(lines); index++ {
		if level := headingLevel(lines[index]); level > 0 && level <= 4 {
			end = index
			break
		}
	}

	body := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
	if body == "" {
		return "", false
	}
	return body, true
}

// campSummary builds the short plain-text camp card shown after camp selection.
func campSummary(knowledge string, selected camp) (string, bool) {
	title, section, ok := campSection(knowledge, selected.heading)
	if !ok {
		return "", false
	}

	summary := title
	if description, ok := subsection(section, "Краткое описание"); ok {
		summary += "\n\n" + description
	}
	if dates, ok := subsection(section, "Даты"); ok {
		summary += "\n\nДаты: " + dates
	}
	return plainText(summary), true
}

func campDetails(knowledge string, selected camp) (string, bool) {
	title, section, ok := campSection(knowledge, selected.heading)
	if !ok {
		return "", false
	}
	return plainText(title + "\n\n" + section), true
}

// campTitle returns the knowledge base title of the selected camp.
func campTitle(knowledge, campID string) string {
	selected, ok := campByID(campID)
	if !ok {
		return ""
	}
	title, _, ok := campSection(knowledge, selected.heading)
	if !ok {
		return selected.heading
	}
	return title
}

// headingLevel returns the Markdown heading level of a line, or 0 for body text.
func headingLevel(line string) int {
	hashes := len(line) - len(strings.TrimLeft(line, "#"))
	if hashes == 0 || !strings.HasPrefix(line[hashes:], " ") {
		return 0
	}
	return hashes
}

// plainText drops Markdown emphasis so Telegram messages can be sent without a parse mode.
func plainText(text string) string {
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "\n> ", "\n")
	lines := strings.Split(strings.TrimPrefix(text, "> "), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if headingLevel(trimmed) > 0 {
			line = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			cells := strings.Split(strings.Trim(trimmed, "|"), "|")
			separator := true
			for index, cell := range cells {
				cells[index] = strings.TrimSpace(cell)
				if strings.Trim(cells[index], " :-") != "" {
					separator = false
				}
			}
			if separator {
				continue
			}
			line = strings.Join(cells, " — ")
		}
		result = append(result, line)
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}
