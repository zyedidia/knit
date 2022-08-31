package rules

import (
	"strings"
)

type Command struct {
	Directory string `json:"directory"`
	File      string `json:"file"`
	Command   string `json:"command"`
}

func Commands(gs *GraphSet) []Command {
	cmds := make([]Command, 0)
	for _, g := range gs.graphs {
		for _, n := range g.nodes {
			for _, p := range n.prereqs {
				if len(p.prereqs) == 0 {
					for _, o := range p.outputs {
						cmds = append(cmds, Command{
							Directory: p.graph.dir,
							File:      o.name,
							Command:   strings.Join(n.recipe, ";"),
						})
					}
				}
			}
		}
	}
	return cmds
}
