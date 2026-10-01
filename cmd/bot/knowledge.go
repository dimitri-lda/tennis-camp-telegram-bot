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

type knowledgeTopic struct {
	id       string
	label    string
	headings []string
}

var trainingTopics = []knowledgeTopic{
	{id: "tennis", label: "Большой теннис", headings: []string{"Форматы и уровни большого тенниса", "Содержание занятий"}},
	{id: "padel", label: "Падел", headings: []string{"Падел"}},
	{id: "children", label: "Детские группы", headings: []string{"Детские группы"}},
	{id: "adults", label: "Взрослые группы", headings: []string{"Взрослые группы"}},
	{id: "coaches", label: "Тренеры", headings: []string{"Тренеры по большому теннису"}},
	{id: "prices", label: "Цены", headings: []string{"Краткий ориентир по ценам"}},
	{id: "locations", label: "Площадки", headings: []string{"Площадки"}},
}

var campTopics = []knowledgeTopic{
	{id: "program", label: "Программа", headings: []string{"Тренировочная программа"}},
	{id: "dates_price", label: "Даты и стоимость", headings: []string{"Даты", "Стоимость", "Что входит в стоимость"}},
	{id: "stay", label: "Проживание", headings: []string{"Проживание"}},
	{id: "coaches", label: "Тренеры", headings: []string{"Тренерская команда"}},
	{id: "practical", label: "Практическая информация"},
}

func campByID(id string) (camp, bool) {
	for _, item := range camps {
		if item.id == id {
			return item, true
		}
	}
	return camp{}, false
}

func topicByID(topics []knowledgeTopic, id string) (knowledgeTopic, bool) {
	for _, topic := range topics {
		if topic.id == id {
			return topic, true
		}
	}
	return knowledgeTopic{}, false
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

func trainingInfo(knowledge string) (string, bool) {
	return sectionIntro(knowledge, sectionTraining)
}

func aboutInfo(knowledge string) (string, bool) {
	return sectionIntro(knowledge, sectionAbout)
}

func sectionIntro(knowledge, title string) (string, bool) {
	lines := strings.Split(knowledge, "\n")
	start := -1
	for index, line := range lines {
		if headingLevel(line) == 2 && strings.TrimSpace(strings.TrimLeft(line, "#")) == title {
			start = index
			break
		}
	}
	if start < 0 {
		return "", false
	}

	end := len(lines)
	for index := start + 1; index < len(lines); index++ {
		if level := headingLevel(lines[index]); level > 0 && level <= 3 {
			end = index
			break
		}
	}
	text := plainText(strings.Join(lines[start:end], "\n"))
	return text, text != ""
}

func sectionBody(document, title string, level int) (string, bool) {
	lines := strings.Split(document, "\n")
	start := -1
	for index, line := range lines {
		if headingLevel(line) == level && strings.TrimSpace(strings.TrimLeft(line, "#")) == title {
			start = index + 1
			break
		}
	}
	if start < 0 {
		return "", false
	}

	end := len(lines)
	for index := start; index < len(lines); index++ {
		if nextLevel := headingLevel(lines[index]); nextLevel > 0 && nextLevel <= level {
			end = index
			break
		}
	}
	body := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
	return body, body != ""
}

func topicInfo(section string, topic knowledgeTopic, level int) (string, bool) {
	parts := make([]string, 0, len(topic.headings))
	for _, heading := range topic.headings {
		body, ok := sectionBody(section, heading, level)
		if !ok {
			continue
		}
		part := heading + "\n" + body
		if len(topic.headings) == 1 && heading == topic.label {
			part = body
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "", false
	}
	return plainText(topic.label + "\n\n" + strings.Join(parts, "\n\n")), true
}

func trainingTopicInfo(knowledge, topicID string) (string, string, bool) {
	topic, ok := topicByID(trainingTopics, topicID)
	if !ok {
		return "", "", false
	}
	section, ok := sectionBody(knowledge, sectionTraining, 2)
	if !ok {
		return "", "", false
	}
	text, ok := topicInfo(section, topic, 3)
	return topic.label, text, ok
}

func campTopicInfo(knowledge string, selected camp, topicID string) (string, string, bool) {
	topic, ok := topicByID(campTopics, topicID)
	if !ok {
		return "", "", false
	}
	if topic.id == "practical" {
		text := "Практическая информация\n\nНапишите вопрос о погоде, одежде, валюте, розетках, связи, безопасности или подготовке к поездке. ИИ-помощник Dzala даст общую справочную информацию о направлении, а не условия кэмпа."
		return topic.label, text, true
	}
	_, section, ok := campSection(knowledge, selected.heading)
	if !ok {
		return "", "", false
	}
	text, ok := topicInfo(section, topic, 4)
	return topic.label, text, ok
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
