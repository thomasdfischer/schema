package diff

import (
	"fmt"
	"os"
	"reflect"

	"github.com/Confbase/schema/decode"
	"github.com/Confbase/schema/example"
	"github.com/Confbase/schema/jsonsch"
)

func Diff(s1, s2 jsonsch.Schema, titles *titleStrings) ([]Difference, error) {
	if titles.Title1 != "" {
		titles.MissFrom1 = titles.Title1
		titles.Differ1 = titles.Title1
	}
	if titles.Title2 != "" {
		titles.MissFrom2 = titles.Title2
		titles.Differ2 = titles.Title2
	}

	return diff(s1, s2, "", titles)
}

func Entry(cfg *Config) {
	f1, err := os.Open(cfg.Schema1)
	nilOrFatal(err)
	f2, err := os.Open(cfg.Schema2)
	nilOrFatal(err)

	map1, err := decode.MuxDecode(f1)
	nilOrFatal(err)
	f1.Close()
	map2, err := decode.MuxDecode(f2)
	nilOrFatal(err)
	f2.Close()

	s1, err := jsonsch.FromSchema(map1, cfg.DoSkipRefs)
	if err != nil {
		params := jsonsch.FromExampleParams{
			DoOmitReq:     false,
			DoMakeReq:     true,
			EmptyArraysAs: "",
			NullAs:        "",
		}
		s1, err = jsonsch.FromExample(example.New(map1), &params)
		nilOrFatal(err)
	}
	s2, err := jsonsch.FromSchema(map2, cfg.DoSkipRefs)
	if err != nil {
		params := jsonsch.FromExampleParams{
			DoOmitReq:     false,
			DoMakeReq:     true,
			EmptyArraysAs: "",
			NullAs:        "",
		}
		s2, err = jsonsch.FromExample(example.New(map2), &params)
		nilOrFatal(err)
	}

	diffs, err := Diff(s1, s2, &cfg.titleStrings)
	nilOrFatal(err)

	for _, d := range diffs {
		fmt.Println(d)
	}
	if len(diffs) != 0 {
		os.Exit(2)
	}
}

func diff(s1, s2 jsonsch.Schema, parentKey string, titles *titleStrings) ([]Difference, error) {
	s1Props, s2Props := s1.GetProperties(), s2.GetProperties()
	s1Diffs, err := diffPropsFrom(s1Props, s2Props, titles)
	if err != nil {
		return nil, err
	}
	switchedTitles := titleStrings{
		Title1:    titles.Title2,
		Title2:    titles.Title1,
		MissFrom1: titles.MissFrom2,
		MissFrom2: titles.MissFrom1,
		Differ1:   titles.Differ2,
		Differ2:   titles.Differ1,
	}
	s2Diffs, err := diffPropsFrom(s2Props, s1Props, &switchedTitles)
	if err != nil {
		return nil, err
	}

	// differingTypes is the set of fields which have differing types.
	// Any DifferyingType found in s1 is guaranteed
	// to be in s2, but we ony want *one* of these instances
	// in the returned diffs.
	diffs, differingTypes := filterUniqueDiffs(s1Diffs, make(map[string]bool))
	diffs2, _ := filterUniqueDiffs(s2Diffs, differingTypes)

	return append(diffs, diffs2...), nil
}

func filterUniqueDiffs(newDiffs []Difference, differingTypes map[string]bool) ([]Difference, map[string]bool) {
	diffs := make([]Difference, 0)
	for _, d := range newDiffs {
		if _, ok := d.(*DifferingTypes); ok {
			field := d.getField()
			if _, ok := differingTypes[field]; !ok {
				diffs = append(diffs, d)
				differingTypes[field] = true
			}
		} else {
			diffs = append(diffs, d)
		}
	}
	return diffs, differingTypes
}

// diffPropsFrom assumes props1 is the base. It will return
// 1. all DifferingTypes differences
// 2. all fields which are in props1 but missing from props2
//
// Therefore, to do a complete diff of props1 and props2,
// one must call
// diffPropsFrom(props1, props2) AND diffPropsFrom(props2, props1)
// and merge the results
func diffPropsFrom(props1, props2 map[string]interface{}, titles *titleStrings) ([]Difference, error) {
	diffs := make([]Difference, 0)
	for k, v1 := range props1 {
		v2, ok := props2[k]
		if !ok {
			diffs = append(diffs, &MissingField{k, titles.MissFrom2})
			continue
		}
		subDiffs, err := diffSomething(v1, v2, k, titles)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, subDiffs...)
	}
	return diffs, nil
}

func diffSomething(v1, v2 interface{}, k string, titles *titleStrings) ([]Difference, error) {
	diffs := make([]Difference, 0)

	type1, err := getType(v1, k)
	if err != nil {
		return nil, err
	}
	type2, err := getType(v2, k)
	if err != nil {
		return nil, err
	}
	if type1 != type2 {
		diffs = append(diffs, &DifferingTypes{
			field:  k,
			title1: titles.Differ1,
			title2: titles.Differ2,
		})
		return diffs, nil
	}

	switch v1.(type) {
	case jsonsch.Primitive:
		return diffs, nil
	case jsonsch.ArraySchema:
		a1, ok := v1.(jsonsch.ArraySchema)
		if !ok {
			return nil, fmt.Errorf("saw type 'array' but internal type is not array")
		}
		a2, ok := v2.(jsonsch.ArraySchema)
		if !ok {
			return nil, fmt.Errorf("saw type 'array' but internal type is not array")
		}
		subDiffs, err := diffSomething(a1.Items, a2.Items, "items", titles)
		if err != nil {
			return nil, err
		}
		for _, d := range subDiffs {
			prependKey(d, k)
			diffs = append(diffs, d)
		}
	case jsonsch.Schema:
		s1, ok := v1.(jsonsch.Schema)
		if !ok {
			return nil, fmt.Errorf("saw type 'object' but internal type is not object")
		}
		s2, ok := v2.(jsonsch.Schema)
		if !ok {
			return nil, fmt.Errorf("saw type 'object' but internal type is not object")
		}
		subDiffs, err := Diff(s1, s2, titles)
		if err != nil {
			return nil, err
		}
		for _, d := range subDiffs {
			prependKey(d, k)
			diffs = append(diffs, d)
		}
	default:
		return nil, fmt.Errorf("key '%v' has unrecognized type '%v'", k, reflect.TypeOf(v1))
	}
	return diffs, nil
}

func getType(schema interface{}, k string) (jsonsch.Type, error) {
	switch v := schema.(type) {
	case jsonsch.Primitive:
		return v.Type, nil
	case jsonsch.ArraySchema:
		return v.Type, nil
	case jsonsch.Schema:
		return v.GetType(), nil
	default:
		return "", fmt.Errorf("key '%v' has unrecognized type '%v'", k, reflect.TypeOf(v))
	}
}

func nilOrFatal(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
