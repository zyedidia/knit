package main

import (
	"log"
	"os"
	"text/template"
)

func main() {
	funcMap := template.FuncMap{
		"read": func(s string) string {
			data, err := os.ReadFile(s)
			if err == nil {
				return string(data)
			}
			return ""
		},
	}

	if len(os.Args) <= 1 {
		log.Fatal("no input file")
	}

	input, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}

	tmpl, err := template.New("replace").Funcs(funcMap).Parse(string(input))
	if err != nil {
		log.Fatalf("parsing: %s", err)
	}

	err = tmpl.Execute(os.Stdout, nil)
	if err != nil {
		log.Fatalf("execution: %s", err)
	}
}
