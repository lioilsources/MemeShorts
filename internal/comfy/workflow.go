package comfy

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Workflow je graf v API formátu ComfyUI. Soubor navíc nese klíč "_params":
// jméno parametru → seznam cílů [node, vstup]. Parametry se dosazují přes Set,
// takže volající nemusí znát čísla nodů.
type Workflow struct {
	Nodes  map[string]*Node
	params map[string][][2]string
}

type Node struct {
	ClassType string         `json:"class_type"`
	Inputs    map[string]any `json:"inputs"`
}

func LoadWorkflow(path string) (Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Workflow{}, err
	}
	wf, err := ParseWorkflow(data)
	if err != nil {
		return wf, fmt.Errorf("%s: %w", path, err)
	}
	return wf, nil
}

func ParseWorkflow(data []byte) (Workflow, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Workflow{}, err
	}
	wf := Workflow{Nodes: map[string]*Node{}, params: map[string][][2]string{}}
	for k, v := range raw {
		if k == "_params" {
			if err := json.Unmarshal(v, &wf.params); err != nil {
				return wf, fmt.Errorf("_params: %w", err)
			}
			continue
		}
		if strings.HasPrefix(k, "_") {
			continue // komentáře
		}
		var n Node
		if err := json.Unmarshal(v, &n); err != nil {
			return wf, fmt.Errorf("node %s: %w", k, err)
		}
		wf.Nodes[k] = &n
	}
	for name, targets := range wf.params {
		for _, t := range targets {
			n, ok := wf.Nodes[t[0]]
			if !ok {
				return wf, fmt.Errorf("_params.%s míří na neexistující node %s", name, t[0])
			}
			if _, ok := n.Inputs[t[1]]; !ok {
				return wf, fmt.Errorf("_params.%s míří na neexistující vstup %s.%s", name, t[0], t[1])
			}
		}
	}
	return wf, nil
}

// Set dosadí parametry; neznámé jméno je chyba (překlep by jinak tiše nechal default).
func (wf Workflow) Set(values map[string]any) error {
	for name, v := range values {
		targets, ok := wf.params[name]
		if !ok {
			return fmt.Errorf("workflow nemá parametr %q (má: %s)", name, strings.Join(wf.Params(), ", "))
		}
		for _, t := range targets {
			wf.Nodes[t[0]].Inputs[t[1]] = v
		}
	}
	return nil
}

func (wf Workflow) Params() []string {
	out := make([]string, 0, len(wf.params))
	for k := range wf.params {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
