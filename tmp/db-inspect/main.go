package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: db-inspect <database> <query>")
		os.Exit(2)
	}
	query := os.Args[2]
	if encoded, ok := strings.CutPrefix(query, "base64:"); ok {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			panic(err)
		}
		query = string(decoded)
	}
	db, err := sql.Open("sqlite", "file:"+os.Args[1]+"?mode=ro")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	rows, err := db.Query(query)
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		panic(err)
	}
	result := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			panic(err)
		}
		item := make(map[string]any, len(columns))
		for index, column := range columns {
			switch value := values[index].(type) {
			case []byte:
				item[column] = string(value)
			default:
				item[column] = value
			}
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}
	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(output))
}
