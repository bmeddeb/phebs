package main

import (
	"encoding/json"
	"errors"
	"fmt"
	pg "github.com/pganalyze/pg_query_go/v6"
	"os"
	"phebs-spike-t261"
)

func frontend(sql, category string) (string, error) {
	raw, err := pg.ParseToJSON(sql)
	if err != nil {
		return "parse-refused", err
	}
	var parsed struct {
		Statements []struct {
			Stmt map[string]json.RawMessage `json:"stmt"`
		} `json:"stmts"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "invalid-ast", err
	}
	if len(parsed.Statements) != 1 || len(parsed.Statements[0].Stmt) != 1 {
		return "invalid-root-count", errors.New("expected one root")
	}
	root := ""
	for k := range parsed.Statements[0].Stmt {
		root = k
	}
	accepted := false
	switch category {
	case "query":
		accepted = root == "SelectStmt" || root == "InsertStmt" || root == "UpdateStmt" || root == "DeleteStmt"
	case "create-table":
		accepted = root == "CreateStmt"
	case "create-index":
		accepted = root == "IndexStmt"
	case "create-view":
		accepted = root == "ViewStmt"
	case "create-type":
		accepted = root == "CreateEnumStmt" || root == "CreateRangeStmt"
	case "create-sequence":
		accepted = root == "CreateSeqStmt"
	case "alter-table":
		accepted = root == "AlterTableStmt"
	case "drop":
		accepted = root == "DropStmt"
	}
	if !accepted {
		return root, errors.New("imported root disagrees with supported category")
	}
	return root, nil
}
func main() {
	if err := reader.Main("postgresql", frontend); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
