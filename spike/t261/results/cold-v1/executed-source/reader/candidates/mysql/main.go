package main

import (
	"errors"
	"fmt"
	parser "github.com/xwb1989/sqlparser"
	"os"
	"phebs-spike-t261"
)

func frontend(sql, category string) (string, error) {
	ast, err := parser.Parse(sql)
	if err != nil {
		return "parse-refused", err
	}
	root := fmt.Sprintf("%T", ast)
	accepted := false
	switch ast.(type) {
	case *parser.Select, *parser.Union, *parser.Insert, *parser.Update, *parser.Delete:
		accepted = category == "query"
	case *parser.DDL:
		accepted = category == "create-table" || category == "create-index" || category == "create-view" || category == "alter-table" || category == "drop"
	}
	if !accepted {
		return root, errors.New("imported root disagrees with supported category")
	}
	return root, nil
}
func main() {
	if err := reader.Main("mysql", frontend); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
