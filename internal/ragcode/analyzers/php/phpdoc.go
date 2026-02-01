package php

import (
	"regexp"
	"strings"

	"github.com/VKCOM/php-parser/pkg/token"
	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
)

// PHPDocInfo contains parsed PHPDoc information
type PHPDocInfo struct {
	Description string
	Params      []ParamDoc
	Returns     []ReturnDoc
	Throws      []string
	Var         string
	VarType     string
	Deprecated  string
	See         []string
	Examples    []string
}

// ParamDoc represents a @param tag
type ParamDoc struct {
	Name        string
	Type        string
	Description string
}

// ReturnDoc represents a @return tag
type ReturnDoc struct {
	Type        string
	Description string
}

// parsePHPDoc extracts PHPDoc information from comment tokens
func parsePHPDoc(tokens []*token.Token) *PHPDocInfo {
	doc := &PHPDocInfo{
		Params:   []ParamDoc{},
		Returns:  []ReturnDoc{},
		Throws:   []string{},
		See:      []string{},
		Examples: []string{},
	}

	// Find T_DOC_COMMENT token
	var docComment string
	for _, tok := range tokens {
		if tok.ID.String() == "T_DOC_COMMENT" {
			docComment = string(tok.Value)
			break
		}
	}

	if docComment == "" {
		return doc
	}

	// Clean up the comment
	lines := strings.Split(docComment, "\n")
	cleanLines := make([]string, 0)

	for _, line := range lines {
		// Remove leading/trailing whitespace
		line = strings.TrimSpace(line)

		// Remove /** and */
		line = strings.TrimPrefix(line, "/**")
		line = strings.TrimSuffix(line, "*/")

		// Remove leading *
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "*") {
			line = strings.TrimPrefix(line, "*")
			line = strings.TrimSpace(line)
		}

		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}

	// Parse lines
	var currentDesc []string
	inDescription := true

	for _, line := range cleanLines {
		if strings.HasPrefix(line, "@") {
			inDescription = false
			parseTag(line, doc)
		} else if inDescription {
			currentDesc = append(currentDesc, line)
		}
	}

	doc.Description = strings.Join(currentDesc, " ")
	return doc
}

// parseTag parses a single PHPDoc tag
func parseTag(line string, doc *PHPDocInfo) {
	// @param type $name description
	paramRe := regexp.MustCompile(`^@param\s+([^\s]+)\s+\$([^\s]+)(?:\s+(.*))?$`)
	if matches := paramRe.FindStringSubmatch(line); matches != nil {
		doc.Params = append(doc.Params, ParamDoc{
			Type:        matches[1],
			Name:        matches[2],
			Description: strings.TrimSpace(matches[3]),
		})
		return
	}

	// @return type description
	returnRe := regexp.MustCompile(`^@return\s+([^\s]+)(?:\s+(.*))?$`)
	if matches := returnRe.FindStringSubmatch(line); matches != nil {
		doc.Returns = append(doc.Returns, ReturnDoc{
			Type:        matches[1],
			Description: strings.TrimSpace(matches[2]),
		})
		return
	}

	// @throws ExceptionClass description
	throwsRe := regexp.MustCompile(`^@throws\s+([^\s]+)(?:\s+(.*))?$`)
	if matches := throwsRe.FindStringSubmatch(line); matches != nil {
		throwDesc := matches[1]
		if matches[2] != "" {
			throwDesc += " - " + strings.TrimSpace(matches[2])
		}
		doc.Throws = append(doc.Throws, throwDesc)
		return
	}

	// @var type description
	varRe := regexp.MustCompile(`^@var\s+([^\s]+)(?:\s+(.*))?$`)
	if matches := varRe.FindStringSubmatch(line); matches != nil {
		doc.VarType = matches[1]
		doc.Var = strings.TrimSpace(matches[2])
		return
	}

	// @deprecated description
	if strings.HasPrefix(line, "@deprecated") {
		doc.Deprecated = strings.TrimSpace(strings.TrimPrefix(line, "@deprecated"))
		return
	}

	// @see reference
	if strings.HasPrefix(line, "@see") {
		doc.See = append(doc.See, strings.TrimSpace(strings.TrimPrefix(line, "@see")))
		return
	}

	// @example code
	if strings.HasPrefix(line, "@example") {
		doc.Examples = append(doc.Examples, strings.TrimSpace(strings.TrimPrefix(line, "@example")))
		return
	}
}

// convertPHPDocToReturnInfo converts PHPDoc returns to codetypes.ReturnInfo
func convertPHPDocToReturnInfo(docReturns []ReturnDoc) []codetypes.ReturnInfo {
	returns := make([]codetypes.ReturnInfo, len(docReturns))
	for i, r := range docReturns {
		returns[i] = codetypes.ReturnInfo{
			Type:        r.Type,
			Description: r.Description,
		}
	}
	return returns
}

// extractPHPDocFromToken extracts PHPDoc from a specific token (for classes)
func extractPHPDocFromToken(tok *token.Token) *PHPDocInfo {
	if tok == nil || tok.FreeFloating == nil {
		return &PHPDocInfo{
			Params:   []ParamDoc{},
			Returns:  []ReturnDoc{},
			Throws:   []string{},
			See:      []string{},
			Examples: []string{},
		}
	}

	return parsePHPDoc(tok.FreeFloating)
}
