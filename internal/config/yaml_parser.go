package config

import (
	"fmt"
	"strconv"
	"strings"
)

// parseYAML parses a minimal subset of YAML 1.2 sufficient for ircat's
// configuration. The result is a tree of map[string]any, []any, and
// scalars (string, int64, float64, bool, nil).
//
// Supported:
//   - block mappings ("key: value", nested by indentation)
//   - block sequences ("- item", with mapping or scalar items)
//   - empty flow markers "{}" and "[]"
//   - bare and quoted scalars (single or double quotes)
//   - integers, floats, booleans (true/false/yes/no/on/off), null/~
//   - line comments starting with '#'
//   - leading "---" document marker (single-document only)
//
// Not supported (and rejected with an error or silently ignored where
// noted): anchors/aliases, tags, complex keys, flow-style maps and
// sequences beyond {}/[], block scalar literals (| / >), multi-doc.
// If we ever need any of these we either grow the parser or fall back
// to the dependency option called out in docs/ARCHITECTURE.md.
func parseYAML(data []byte) (any, error) {
	p := newYAMLParser(data)
	return p.parseDocument()
}

type yamlLine struct {
	num    int    // 1-based, for error messages
	indent int    // count of leading spaces (tabs are rejected)
	text   string // line content with indent and trailing whitespace stripped
}

type yamlParser struct {
	lines []yamlLine
	pos   int
}

func newYAMLParser(data []byte) *yamlParser {
	raw := strings.Split(string(data), "\n")
	out := make([]yamlLine, 0, len(raw))
	for i, line := range raw {
		// Strip CR for CRLF inputs.
		line = strings.TrimRight(line, "\r")
		// Skip blank and pure-comment lines.
		stripped := stripComment(line)
		if strings.TrimSpace(stripped) == "" {
			continue
		}
		indent, content, ok := splitIndent(stripped)
		if !ok {
			// Tabs encountered — reject loudly.
			out = append(out, yamlLine{num: i + 1, indent: -1, text: line})
			continue
		}
		out = append(out, yamlLine{num: i + 1, indent: indent, text: content})
	}
	return &yamlParser{lines: out}
}

// stripComment removes a "#" comment from a line, but only when "#"
// appears outside of any quoted span. Quoting is intentionally
// minimal — single and double quotes only, no escapes besides \\
// inside double quotes.
func stripComment(line string) string {
	inSingle := false
	inDouble := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && inDouble && i+1 < len(line):
			i++
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '#' && !inSingle && !inDouble:
			// A comment must be preceded by whitespace or be at the
			// very start of the line; otherwise it's part of a bare
			// scalar like "a#b".
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				return line[:i]
			}
		}
	}
	return line
}

func splitIndent(line string) (int, string, bool) {
	indent := 0
	for indent < len(line) {
		c := line[indent]
		if c == ' ' {
			indent++
			continue
		}
		if c == '\t' {
			return 0, "", false
		}
		break
	}
	return indent, strings.TrimRight(line[indent:], " \t"), true
}

func (p *yamlParser) parseDocument() (any, error) {
	if len(p.lines) == 0 {
		return nil, nil
	}
	if p.lines[0].text == "---" {
		p.pos = 1
	}
	for p.pos < len(p.lines) && p.lines[p.pos].text == "---" {
		// Reject extra document markers.
		return nil, fmt.Errorf("line %d: multi-document YAML is not supported", p.lines[p.pos].num)
	}
	if p.pos >= len(p.lines) {
		return nil, nil
	}
	return p.parseBlock(p.lines[p.pos].indent)
}

// parseBlock parses a block (either mapping or sequence) at the given
// indent. Consumes lines until it hits a line with smaller indent.
func (p *yamlParser) parseBlock(indent int) (any, error) {
	if p.pos >= len(p.lines) {
		return nil, nil
	}
	if p.lines[p.pos].indent != indent {
		return nil, fmt.Errorf("line %d: expected indent %d, got %d", p.lines[p.pos].num, indent, p.lines[p.pos].indent)
	}
	if strings.HasPrefix(p.lines[p.pos].text, "- ") || p.lines[p.pos].text == "-" {
		return p.parseSequence(indent)
	}
	return p.parseMapping(indent)
}

func (p *yamlParser) parseMapping(indent int) (map[string]any, error) {
	out := make(map[string]any)
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, fmt.Errorf("line %d: unexpected indent %d (expected %d)", line.num, line.indent, indent)
		}
		if line.indent == -1 {
			return nil, fmt.Errorf("line %d: tab indentation is not supported", line.num)
		}
		if strings.HasPrefix(line.text, "- ") || line.text == "-" {
			return nil, fmt.Errorf("line %d: sequence item inside a mapping at the same indent", line.num)
		}
		key, rest, err := splitMappingKey(line.text)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line.num, err)
		}
		p.pos++
		value, err := p.parseValue(indent, rest)
		if err != nil {
			return nil, err
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("line %d: duplicate key %q", line.num, key)
		}
		out[key] = value
	}
	return out, nil
}

func (p *yamlParser) parseSequence(indent int) ([]any, error) {
	var out []any
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		if line.indent < indent {
			break
		}
		if line.indent != indent {
			return nil, fmt.Errorf("line %d: unexpected indent %d (expected %d)", line.num, line.indent, indent)
		}
		if !strings.HasPrefix(line.text, "- ") && line.text != "-" {
			break
		}
		var rest string
		if line.text == "-" {
			rest = ""
		} else {
			rest = strings.TrimLeft(line.text[1:], " ")
		}
		p.pos++

		// Three cases for a sequence item:
		//   1) "- value"           — inline scalar value
		//   2) "- key: value..."   — inline first kv of a mapping; the
		//                            rest of the mapping continues on
		//                            following lines indented further.
		//   3) "-" (then nested)   — block on the next lines.
		if rest == "" {
			val, err := p.parseValueAfterDash(indent)
			if err != nil {
				return nil, err
			}
			out = append(out, val)
			continue
		}
		if k, v, ok := tryMappingKey(rest); ok {
			// Build a mapping starting with this inline kv. The
			// continuation lives at indent+2 (the space after "- ").
			childIndent := indent + 2
			mapping := make(map[string]any)
			value, err := p.parseValue(childIndent, v)
			if err != nil {
				return nil, err
			}
			mapping[k] = value
			// Consume more keys at the same childIndent.
			for p.pos < len(p.lines) && p.lines[p.pos].indent == childIndent && !strings.HasPrefix(p.lines[p.pos].text, "- ") {
				moreLine := p.lines[p.pos]
				mk, mr, err := splitMappingKey(moreLine.text)
				if err != nil {
					return nil, fmt.Errorf("line %d: %w", moreLine.num, err)
				}
				p.pos++
				mv, err := p.parseValue(childIndent, mr)
				if err != nil {
					return nil, err
				}
				if _, dup := mapping[mk]; dup {
					return nil, fmt.Errorf("line %d: duplicate key %q", moreLine.num, mk)
				}
				mapping[mk] = mv
			}
			out = append(out, mapping)
			continue
		}
		// Plain inline scalar.
		out = append(out, parseScalar(rest))
	}
	return out, nil
}

// parseValue handles whatever follows a "key:" on the same line.
// rest is the trimmed text after the colon and may be empty (in which
// case the value is a nested block on the following lines).
func (p *yamlParser) parseValue(parentIndent int, rest string) (any, error) {
	if rest == "" {
		return p.parseNestedBlock(parentIndent)
	}
	switch rest {
	case "{}":
		return map[string]any{}, nil
	case "[]":
		return []any{}, nil
	}
	return parseScalar(rest), nil
}

// parseValueAfterDash handles a bare "-" line whose value lives on
// indented lines below.
func (p *yamlParser) parseValueAfterDash(parentIndent int) (any, error) {
	if p.pos >= len(p.lines) || p.lines[p.pos].indent <= parentIndent {
		return nil, nil
	}
	return p.parseBlock(p.lines[p.pos].indent)
}

func (p *yamlParser) parseNestedBlock(parentIndent int) (any, error) {
	if p.pos >= len(p.lines) {
		return nil, nil
	}
	next := p.lines[p.pos]
	if next.indent <= parentIndent {
		// No nested block — value is null.
		return nil, nil
	}
	return p.parseBlock(next.indent)
}

// splitMappingKey extracts the key from a "key: value" line. Quoted
// keys are supported. The colon must be followed by EOL or whitespace.
func splitMappingKey(line string) (string, string, error) {
	k, rest, ok := tryMappingKey(line)
	if !ok {
		return "", "", fmt.Errorf("expected mapping entry, got %q", line)
	}
	return k, rest, nil
}

func tryMappingKey(line string) (string, string, bool) {
	// Quoted key
	if len(line) > 0 && (line[0] == '"' || line[0] == '\'') {
		quote := line[0]
		end := -1
		for i := 1; i < len(line); i++ {
			if line[i] == '\\' && quote == '"' && i+1 < len(line) {
				i++
				continue
			}
			if line[i] == quote {
				end = i
				break
			}
		}
		if end < 0 || end+1 >= len(line) || line[end+1] != ':' {
			return "", "", false
		}
		key := unquote(line[:end+1])
		rest := strings.TrimLeft(line[end+2:], " \t")
		return key, rest, true
	}
	// Bare key: search for the first ':' that is followed by EOL or whitespace.
	for i := 0; i < len(line); i++ {
		if line[i] != ':' {
			continue
		}
		if i+1 == len(line) || line[i+1] == ' ' || line[i+1] == '\t' {
			key := strings.TrimRight(line[:i], " \t")
			if key == "" {
				return "", "", false
			}
			rest := ""
			if i+1 < len(line) {
				rest = strings.TrimLeft(line[i+1:], " \t")
			}
			return key, rest, true
		}
	}
	return "", "", false
}

// parseScalar converts a YAML scalar string into a Go value.
// Recognized: integers, floats, booleans, null, single/double quoted
// strings. Anything else is returned as a plain string.
func parseScalar(s string) any {
	if len(s) >= 2 {
		switch s[0] {
		case '"':
			if s[len(s)-1] == '"' {
				return unquote(s)
			}
		case '\'':
			if s[len(s)-1] == '\'' {
				return unquote(s)
			}
		}
	}
	switch strings.ToLower(s) {
	case "null", "~":
		return nil
	case "true", "yes", "on":
		return true
	case "false", "no", "off":
		return false
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// unquote strips a single layer of quotes from a YAML scalar.
// Single-quoted: '' becomes '. Double-quoted: \\ \" \n \t \r are
// recognized; any other escape is returned literally without the
// backslash.
func unquote(s string) string {
	if len(s) < 2 {
		return s
	}
	q := s[0]
	if s[len(s)-1] != q || (q != '\'' && q != '"') {
		return s
	}
	body := s[1 : len(s)-1]
	if q == '\'' {
		return strings.ReplaceAll(body, "''", "'")
	}
	var b strings.Builder
	b.Grow(len(body))
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			b.WriteByte(body[i])
			continue
		}
		i++
		switch body[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte(body[i])
		}
	}
	return b.String()
}
