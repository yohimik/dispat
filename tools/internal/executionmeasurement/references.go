package executionmeasurement

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const (
	measurementSource      = "measurement"
	evidenceSource         = "evidence"
	pointerSeparator       = "/"
	pointerEscape          = "~"
	pointerSlashEscape     = "~1"
	pointerTildeEscape     = "~0"
	invalidPointerFormat   = "measurement pointer %q does not resolve"
	invalidReferenceFormat = "measurement reference has invalid source or evidence: %+v"
)

func document(value any) (any, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result any
	err = json.Unmarshal(body, &result)
	return result, err
}

func resolve(root any, pointer string) (any, error) {
	if pointer == "" {
		return root, nil
	}
	if !strings.HasPrefix(pointer, pointerSeparator) {
		return nil, fmt.Errorf(invalidPointerFormat, pointer)
	}
	value := root
	for _, token := range strings.Split(pointer[1:], pointerSeparator) {
		for index := 0; index < len(token); index++ {
			if token[index:index+1] == pointerEscape {
				if index+1 >= len(token) || (token[index:index+2] != pointerSlashEscape && token[index:index+2] != pointerTildeEscape) {
					return nil, fmt.Errorf(invalidPointerFormat, pointer)
				}
				index++
			}
		}
		token = strings.ReplaceAll(strings.ReplaceAll(token, pointerSlashEscape, pointerSeparator), pointerTildeEscape, pointerEscape)
		switch container := value.(type) {
		case map[string]any:
			var exists bool
			value, exists = container[token]
			if !exists {
				return nil, fmt.Errorf(invalidPointerFormat, pointer)
			}
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(container) || strconv.Itoa(index) != token {
				return nil, fmt.Errorf(invalidPointerFormat, pointer)
			}
			value = container[index]
		default:
			return nil, fmt.Errorf(invalidPointerFormat, pointer)
		}
	}
	return value, nil
}

func validateReference(root any, reference Reference, evidence map[string]bool) error {
	switch reference.Source {
	case measurementSource:
		if reference.EvidenceID != "" {
			return fmt.Errorf(invalidReferenceFormat, reference)
		}
		_, err := resolve(root, reference.Pointer)
		return err
	case evidenceSource:
		if !evidence[reference.EvidenceID] || (reference.Pointer != "" && !strings.HasPrefix(reference.Pointer, pointerSeparator)) {
			return fmt.Errorf(invalidReferenceFormat, reference)
		}
		return nil
	default:
		return fmt.Errorf(invalidReferenceFormat, reference)
	}
}

func measurementReference(pointer string) Reference {
	return Reference{Source: measurementSource, Pointer: pointer}
}
