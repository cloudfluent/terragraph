package language

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// Token ranges preserve editor offsets while braces in quoted strings, comments and heredocs stay out of the block stack.
func contextTokens(text []byte) hclsyntax.Tokens {
	tokens, _ := hclsyntax.LexConfig(text, "", hcl.InitialPos)
	result := make(hclsyntax.Tokens, 0, len(tokens))
	for _, token := range tokens {
		if token.Type != hclsyntax.TokenComment && token.Type != hclsyntax.TokenNewline && token.Type != hclsyntax.TokenEOF {
			result = append(result, token)
		}
	}
	return result
}

type openBlock struct {
	name, label, attribute string
	at                     int
}

func headerAt(tokens hclsyntax.Tokens, index int) openBlock {
	block := openBlock{at: tokens[index].Range.Start.Byte}
	if index == 0 {
		return block
	}
	previous := tokens[index-1]
	if previous.Type == hclsyntax.TokenEqual && index >= 2 && tokens[index-2].Type == hclsyntax.TokenIdent {
		block.attribute = string(tokens[index-2].Bytes)
	} else if previous.Type == hclsyntax.TokenIdent {
		block.name = string(previous.Bytes)
		if index >= 2 && tokens[index-2].Type == hclsyntax.TokenIdent {
			block.name = string(tokens[index-2].Bytes)
			block.label = string(previous.Bytes)
		}
	} else if previous.Type == hclsyntax.TokenCQuote {
		opening := index - 2
		if opening >= 0 && tokens[opening].Type == hclsyntax.TokenQuotedLit {
			block.label, _ = hclsyntax.ParseStringLiteralToken(tokens[opening])
			opening--
		}
		if opening >= 1 && tokens[opening].Type == hclsyntax.TokenOQuote && tokens[opening-1].Type == hclsyntax.TokenIdent {
			block.name = string(tokens[opening-1].Bytes)
		}
	}
	return block
}

func openBlocksAt(text []byte, offset int) []openBlock {
	stack := []openBlock{}
	tokens := contextTokens(text)
	for index, token := range tokens {
		if token.Range.Start.Byte >= offset {
			break
		}
		switch token.Type {
		case hclsyntax.TokenOBrace:
			stack = append(stack, headerAt(tokens, index))
		case hclsyntax.TokenCBrace:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return stack
}

func blockPathAt(text []byte, offset int) []string {
	path := []string{}
	for _, block := range openBlocksAt(text, offset) {
		if block.name != "" {
			path = append(path, block.name)
		}
	}
	return path
}

func groupAt(text []byte, offset int) string {
	for _, block := range openBlocksAt(text, offset) {
		if block.name == "group" {
			return block.label
		}
	}
	return ""
}

func nodeAt(text []byte, offset int) string {
	for _, block := range openBlocksAt(text, offset) {
		if block.name == "node" {
			return block.label
		}
	}
	return ""
}

func objectAttributeAt(text []byte, offset int) (string, bool) {
	stack := openBlocksAt(text, offset)
	if len(stack) > 0 {
		block := stack[len(stack)-1]
		return block.attribute, block.name == ""
	}
	return "", false
}

func directionAt(text []byte, offset int) string {
	start := 0
	for _, block := range openBlocksAt(text, offset) {
		if block.name != "" {
			start = block.at
		}
	}
	tokens := contextTokens(text)
	direction := ""
	for index, token := range tokens {
		if token.Range.Start.Byte >= offset {
			break
		}
		if token.Range.Start.Byte > start && token.Type == hclsyntax.TokenEqual && index > 0 && tokens[index-1].Type == hclsyntax.TokenIdent {
			direction = string(tokens[index-1].Bytes)
		}
	}
	return direction
}

func literalAt(text []byte, offset int) bool {
	tokens, _ := hclsyntax.LexConfig(text, "", hcl.InitialPos)
	for _, token := range tokens {
		if offset <= token.Range.Start.Byte || offset > token.Range.End.Byte {
			continue
		}
		switch token.Type {
		case hclsyntax.TokenComment:
			if offset < token.Range.End.Byte || len(token.Bytes) == 0 || token.Bytes[len(token.Bytes)-1] != '\n' {
				return true
			}
		case hclsyntax.TokenQuotedLit, hclsyntax.TokenStringLit, hclsyntax.TokenOQuote, hclsyntax.TokenOHeredoc:
			return true
		}
	}
	return false
}

func enclosingNamedBlock(text []byte, offset int, name string) (start, end int, ok bool) {
	start = -1
	for _, block := range openBlocksAt(text, offset) {
		if block.name == name {
			start = block.at
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	depth := 0
	for _, token := range contextTokens(text) {
		if token.Range.Start.Byte < start {
			continue
		}
		switch token.Type {
		case hclsyntax.TokenOBrace:
			depth++
		case hclsyntax.TokenCBrace:
			depth--
			if depth == 0 {
				return start, token.Range.Start.Byte, true
			}
		}
	}
	return start, len(text), true
}

func enclosingSyntaxBlock(text []byte, offset int, name string) *hclsyntax.Block {
	start, end, ok := enclosingNamedBlock(text, offset, name)
	if !ok {
		return nil
	}
	header := name + " "
	if name == "use" {
		header += "\"instance\" "
	}
	file, _ := hclsyntax.ParseConfig([]byte(header+string(text[start:end])+"}"), "", hcl.InitialPos)
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok || len(body.Blocks) == 0 {
		return nil
	}
	return body.Blocks[0]
}

func useAsAt(text []byte, offset int) string {
	block := enclosingSyntaxBlock(text, offset, "use")
	if block == nil {
		return ""
	}
	return literalAttribute(block, "as")
}

func edgeEndpointPorts(model workspaceModel, text []byte, offset int, side string) (ports, bool) {
	block := enclosingSyntaxBlock(text, offset, "edge")
	if block == nil {
		return ports{}, false
	}
	return blockEndpointPorts(model, block, side)
}

func edgeInputLabelAt(text []byte, offset int, path []string) (int, bool) {
	if len(path) == 0 || path[len(path)-1] != "edge" {
		return 0, false
	}
	tokens := contextTokens(text)
	for index, token := range tokens {
		if token.Type != hclsyntax.TokenOQuote || index == 0 || tokens[index-1].Type != hclsyntax.TokenIdent || string(tokens[index-1].Bytes) != "input" {
			continue
		}
		if offset < token.Range.End.Byte {
			continue
		}
		end := len(text)
		for _, next := range tokens[index+1:] {
			if next.Type == hclsyntax.TokenCQuote {
				end = next.Range.Start.Byte
				break
			}
		}
		if offset <= end {
			return token.Range.End.Byte, true
		}
	}
	return 0, false
}
