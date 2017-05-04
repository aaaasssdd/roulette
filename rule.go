package roulette

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

// Ruleset ...
type Ruleset interface {
	Execute(vals interface{})
}

type ruleConfig struct {
	delimLeft    string
	delimRight   string
	defaultfuncs template.FuncMap
	userfuncs    template.FuncMap
	allfuncs     template.FuncMap

	expectTypes    []string
	expectTypesErr error
	noResultFunc   bool

	template    *template.Template
	templateErr error
}

// Rule is a single rule expression. A rule expression is a valid go text/template
type Rule struct {
	Name     string `xml:"name,attr"`
	Priority int    `xml:"priority,attr"`
	Expr     string `xml:",innerxml"`
	config   ruleConfig
}

func (r Rule) isValid(filterTypesArr []string) error {

	if r.config.expectTypes == nil || filterTypesArr == nil {
		return r.config.expectTypesErr
	}

	if len(filterTypesArr) == 1 && filterTypesArr[0] == "map[string]interface {}" {
		return nil
	}

	// less
	if len(filterTypesArr) < len(r.config.expectTypes) {
		return r.config.expectTypesErr
	}

	// equal to
	if len(filterTypesArr) == len(r.config.expectTypes) {
		for i := range r.config.expectTypes {
			if filterTypesArr[i] != r.config.expectTypes[i] {
				return r.config.expectTypesErr
			}
		}
	}

	// greater than
	// all expected types should be present in the template data.
	for _, expectedType := range r.config.expectTypes {
		j := sort.SearchStrings(filterTypesArr, expectedType)
		found := j < len(filterTypesArr) && filterTypesArr[j] == expectedType
		if !found {
			return r.config.expectTypesErr
		}
	}

	return nil
}

type textTemplateRulesetConfig struct {
	result         Result
	filterTypesArr []string
	workflowMatch  bool
}

// TextTemplateRuleset is a collection of rules for a valid go type
type TextTemplateRuleset struct {
	Name            string `xml:"name,attr"`
	FilterTypes     string `xml:"filterTypes,attr"`
	FilterStrict    bool   `xml:"filterStrict,attr"`
	DataKey         string `xml:"dataKey,attr"`
	ResultKey       string `xml:"resultKey,attr"`
	Rules           []Rule `xml:"rule"`
	PrioritiesCount string `xml:"prioritiesCount,attr"`
	Workflow        string `xml:"workflow,attr"`
	config          textTemplateRulesetConfig
	bytesBuf        *bytesPool
	mapBuf          *mapPool
	limit           int
}

// sort rules by priority
func (t TextTemplateRuleset) Len() int {
	return len(t.Rules)
}
func (t TextTemplateRuleset) Swap(i, j int) {
	t.Rules[i], t.Rules[j] = t.Rules[j], t.Rules[i]
}
func (t TextTemplateRuleset) Less(i, j int) bool {
	return t.Rules[i].Priority < t.Rules[j].Priority
}

func (t TextTemplateRuleset) isValidForTypes(filterTypesArr ...string) bool {

	if len(filterTypesArr) == 0 {
		return false
	}

	if len(t.config.filterTypesArr) != len(filterTypesArr) && t.FilterStrict {
		return false
	}

	// if not filterStrict, look for atleast one match
	// if filterStrict look for atleast one mismatch
	for i, v := range t.config.filterTypesArr {
		j := sort.SearchStrings(t.config.filterTypesArr, v)
		found := j < len(t.config.filterTypesArr) && t.config.filterTypesArr[i] == v
		if !found {
			if t.FilterStrict {
				return false
			}
		} else {

			// filtering is not strict and one atleast one match found.
			if !t.FilterStrict {
				return true
			}
		}
	}

	return false
}

func (t TextTemplateRuleset) getTypes(vals interface{}) []string {
	var types []string
	switch vals.(type) {
	case []interface{}:
		var typeName string
		size := len(vals.([]interface{}))
		types = make([]string, size)
		for i, v := range vals.([]interface{}) {
			if reflect.ValueOf(v).Kind() == reflect.Ptr || reflect.ValueOf(v).Kind() == reflect.Interface {
				typeName = reflect.TypeOf(v).Elem().String()
			} else {
				typeName = reflect.TypeOf(v).String()
			}

			types[i] = typeName
		}

		break

	default:
		types = make([]string, 1)
		typeName := reflect.TypeOf(vals).Elem().String()
		types[0] = typeName
	}

	return types
}

func (t TextTemplateRuleset) getTemplateData(tmplData map[string]interface{}, vals interface{}) {

	//fmt.Println("getTemplateData", reflect.TypeOf(vals))
	// flatten multiple types in template map so that they can be referred by
	// dataKey

	valsData := t.mapBuf.get()
	defer t.mapBuf.put(valsData)
	// index array of same types
	typeArrayIndex := make(map[string]int)
	var pkgPaths []string

	switch vals.(type) {
	case []interface{}:
		nestedMap := t.mapBuf.get()
		defer t.mapBuf.put(nestedMap)

		for i, val := range vals.([]interface{}) {

			switch val.(type) {
			case []string, []int, []int32, []int64, []bool, []float32, []float64, []interface{}:
				typ := reflect.TypeOf(val).String()
				typeName := strings.Trim(typ, "[]")
				typeName = strings.Trim(typ, "{}")
				valsData[typeName+"slice"+strconv.Itoa(i)] = val

				break

			case map[string]int, map[string]string, map[string]bool:
				typ := reflect.TypeOf(val).String()
				typeName := strings.Trim(typ, "[")
				typeName = strings.Trim(typ, "]")
				valsData[typeName+strconv.Itoa(i)] = val
				break
			case map[string]interface{}:
				valsData = val.(map[string]interface{})
				break

			case bool, int, int32, int64, float32, float64:
				typeName := reflect.TypeOf(val).String()
				valsData[typeName+strconv.Itoa(i)] = val
				break

			default:
				var typeName string
				if reflect.ValueOf(val).Kind() == reflect.Ptr || reflect.ValueOf(val).Kind() == reflect.Interface {
					typeName = reflect.TypeOf(val).Elem().String()
				} else {
					typeName = reflect.TypeOf(val).String()
				}

				var pkgTypeName string
				if reflect.TypeOf(val).PkgPath() != "" {
					pkgTypeName = reflect.TypeOf(val).PkgPath()
				} else {
					pkgPaths = strings.Split(typeName, ".")
					pkgTypeName = pkgPaths[len(pkgPaths)-1]
				}

				indexPkgTypeName := pkgTypeName

				_, ok := typeArrayIndex[pkgTypeName]
				if !ok {
					typeArrayIndex[pkgTypeName] = 0
					nestedMap[pkgTypeName] = val

				} else {
					typeArrayIndex[pkgTypeName]++
				}

				indexPkgTypeName = pkgTypeName + strconv.Itoa(typeArrayIndex[pkgTypeName])
				nestedMap[indexPkgTypeName] = val

				packagePath := ""
				for _, p := range pkgPaths[:len(pkgPaths)-1] {
					packagePath = packagePath + p
				}
				//	fmt.Println("packagePath", packagePath)
				valsData[packagePath] = nestedMap
			}

		}

		break
	default:
		typeName := reflect.TypeOf(vals).Elem().String()
		pkgPaths = strings.Split(typeName, ".")
		//fmt.Println("default", pkgPaths)
		valsData[pkgPaths[0]] = map[string]interface{}{
			pkgPaths[1]: vals,
		}
	}

	valsData[t.ResultKey] = t.config.result
	tmplData[t.DataKey] = valsData

	//fmt.Println("map", tmplData)

}

// Execute ...
func (t TextTemplateRuleset) Execute(vals interface{}) {

	if !t.config.workflowMatch {
		//log.Warnf("ruleset %s is not valid for the current parser %s %s", t.Name, t.Workflow)
		return
	}

	types := t.getTypes(vals)
	sort.Strings(types)
	if !t.isValidForTypes(types...) {
		//	log.Warnf("invalid types %s skipping ruleset %s", types, t.Name)
		return
	}

	types = types[:0]

	//	fmt.Println("types:", types)
	tmplData := t.mapBuf.get()
	defer t.mapBuf.put(tmplData)
	t.getTemplateData(tmplData, vals)

	successCount := 0

	for _, rule := range t.Rules {

		if rule.config.noResultFunc {
			//log.Warnf("rule expression contains result func but no type Result interface was set %s", rule.Name)
			continue
		}

		// validate if one of the types exist in the expression.
		err := rule.isValid(types)
		if err != nil {
			//	log.Warnf("invalid rule %s, error: %v", rule.Name, err)
			continue
		}

		if rule.config.templateErr != nil {
			//	log.Warnf("invalid rule %s, error: %v", rule.Name, rule.config.templateErr)
			continue
		}

		buf := t.bytesBuf.get()
		defer t.bytesBuf.put(buf)

		err = rule.config.template.Execute(buf, tmplData)
		if err != nil {
			//	log.Warnf("invalid rule %s, error: %v", rule.Name, err)
			continue
		}

		//log.Infof("matched rule %s", rule.Name)

		var result bool

		res := strings.TrimSpace(buf.String())

		result, err = strconv.ParseBool(res)
		if err != nil {
			//log.Warnf("parse result error", err)
			continue
		}

		// n high priority rules successful, break
		if result {
			successCount++
			if successCount == t.limit {
				break
			}
		}

	}
}
