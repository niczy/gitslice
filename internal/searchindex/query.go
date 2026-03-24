package searchindex

import (
	"fmt"
	"regexp/syntax"
	"strings"
)

type QueryNodeKind string

const (
	QueryNodeTrue QueryNodeKind = "TRUE"
	QueryNodeAnd  QueryNodeKind = "AND"
	QueryNodeOr   QueryNodeKind = "OR"
	QueryNodeTerm QueryNodeKind = "TERM"
)

type QueryNode struct {
	Kind     QueryNodeKind
	Literal  string
	NGrams   []string
	Children []*QueryNode
}

func BuildRegexQuery(pattern string, weighter BigramWeighter, mode SparseMode) (*QueryNode, error) {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, err
	}
	re = re.Simplify()
	return buildRegexQueryNode(re, weighter, mode), nil
}

func (n *QueryNode) String() string {
	if n == nil {
		return "<nil>"
	}
	switch n.Kind {
	case QueryNodeTrue:
		return "TRUE"
	case QueryNodeTerm:
		return fmt.Sprintf("%s(%q)", n.Kind, n.Literal)
	default:
		parts := make([]string, 0, len(n.Children))
		for _, child := range n.Children {
			parts = append(parts, child.String())
		}
		return fmt.Sprintf("%s(%s)", n.Kind, strings.Join(parts, ","))
	}
}

func buildRegexQueryNode(re *syntax.Regexp, weighter BigramWeighter, mode SparseMode) *QueryNode {
	if re == nil {
		return &QueryNode{Kind: QueryNodeTrue}
	}

	switch re.Op {
	case syntax.OpLiteral:
		if re.Flags&syntax.FoldCase != 0 {
			return &QueryNode{Kind: QueryNodeTrue}
		}
		literal := string(re.Rune)
		if literal == "" {
			return &QueryNode{Kind: QueryNodeTrue}
		}
		return &QueryNode{
			Kind:    QueryNodeTerm,
			Literal: literal,
			NGrams:  UniqueNGramValues(BuildSparseNGrams([]byte(literal), weighter, mode)),
		}
	case syntax.OpCapture:
		if len(re.Sub) == 0 {
			return &QueryNode{Kind: QueryNodeTrue}
		}
		return buildRegexQueryNode(re.Sub[0], weighter, mode)
	case syntax.OpConcat:
		children := make([]*QueryNode, 0, len(re.Sub))
		for _, sub := range re.Sub {
			child := buildRegexQueryNode(sub, weighter, mode)
			if child == nil || child.Kind == QueryNodeTrue {
				continue
			}
			if child.Kind == QueryNodeAnd {
				children = append(children, child.Children...)
				continue
			}
			children = append(children, child)
		}
		return wrapLogicalNode(QueryNodeAnd, children)
	case syntax.OpAlternate:
		children := make([]*QueryNode, 0, len(re.Sub))
		for _, sub := range re.Sub {
			child := buildRegexQueryNode(sub, weighter, mode)
			if child == nil || child.Kind == QueryNodeTrue {
				return &QueryNode{Kind: QueryNodeTrue}
			}
			if child.Kind == QueryNodeOr {
				children = append(children, child.Children...)
				continue
			}
			children = append(children, child)
		}
		return wrapLogicalNode(QueryNodeOr, children)
	case syntax.OpStar, syntax.OpQuest:
		return &QueryNode{Kind: QueryNodeTrue}
	case syntax.OpPlus:
		if len(re.Sub) == 0 {
			return &QueryNode{Kind: QueryNodeTrue}
		}
		return buildRegexQueryNode(re.Sub[0], weighter, mode)
	case syntax.OpRepeat:
		if re.Min == 0 || len(re.Sub) == 0 {
			return &QueryNode{Kind: QueryNodeTrue}
		}
		return buildRegexQueryNode(re.Sub[0], weighter, mode)
	case syntax.OpEmptyMatch,
		syntax.OpAnyCharNotNL,
		syntax.OpAnyChar,
		syntax.OpCharClass,
		syntax.OpBeginLine,
		syntax.OpEndLine,
		syntax.OpBeginText,
		syntax.OpEndText,
		syntax.OpWordBoundary,
		syntax.OpNoWordBoundary:
		return &QueryNode{Kind: QueryNodeTrue}
	default:
		return &QueryNode{Kind: QueryNodeTrue}
	}
}

func wrapLogicalNode(kind QueryNodeKind, children []*QueryNode) *QueryNode {
	if len(children) == 0 {
		return &QueryNode{Kind: QueryNodeTrue}
	}
	if len(children) == 1 {
		return children[0]
	}
	return &QueryNode{Kind: kind, Children: children}
}
