package schema

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"time"

	"github.com/lestrrat/go-jspointer"
	"github.com/lestrrat/go-pdebug"
	"github.com/lestrrat/go-structinfo"
)

// This is used to check against result of reflect.MapIndex
var zeroval = reflect.Value{}

func New() *Schema {
	s := &Schema{
		cachedReference: make(map[string]interface{}),
		schemaByID:      make(map[string]*Schema),
	}
	return s
}

func Read(in io.Reader) (*Schema, error) {
	s := New()
	dec := json.NewDecoder(in)
	if err := dec.Decode(s); err != nil {
		return nil, err
	}

	s.applyParentSchema()
	return s, nil
}

func (s Schema) IsEmpty() bool {
	if s.Title != "" {
		return false
	}
	if s.Description != "" {
		return false
	}
	if s.Default != nil {
		return false
	}
	if len(s.Type) > 0 {
		return false
	}
	if s.SchemaRef != "" {
		return false
	}

	if len(s.Definitions) > 0 {
		return false
	}
	if s.Reference != "" {
		return false
	}
	if s.Format != "" {
		return false
	}

	if s.MultipleOf.Initialized {
		return false
	}
	if s.Minimum.Initialized {
		return false
	}
	if s.Maximum.Initialized {
		return false
	}
	if s.ExclusiveMinimum.Initialized {
		return false
	}
	if s.ExclusiveMaximum.Initialized {
		return false
	}
	if s.MaxLength.Initialized {
		return false
	}
	if s.MinLength.Initialized {
		return false
	}
	if s.Pattern != nil {
		return false
	}

	// ArrayValidations
	//	AllowAdditionalItems bool
	//	AdditionalItems      []*Schema
	//	Items                []*Schema
	//	minItems             integer
	//	maxItems             integer

	if s.UniqueItems.Initialized {
		return false
	}
	if s.MaxProperties.Initialized {
		return false
	}
	if s.MinProperties.Initialized {
		return false
	}
	if s.Required != nil {
		return false
	}
	if s.Properties != nil {
		return false
	}
	if s.AdditionalProperties == nil || !s.AdditionalProperties.IsEmpty() {
		return false
	}
	if len(s.PatternProperties) > 0 {
		return false
	}
	if len(s.Enum) > 0 {
		return false
	}
	if len(s.AllOf) > 0 {
		return false
	}
	if len(s.AnyOf) > 0 {
		return false
	}
	if len(s.OneOf) > 0 {
		return false
	}
	if s.Not != nil {
		return false
	}
	return true
}

func (s *Schema) setParent(v *Schema) {
	s.parent = v
}

func (s *Schema) applyParentSchema() {
	// Find all components that may be a Schema
	for _, v := range s.Definitions {
		v.setParent(s)
		v.applyParentSchema()
	}

	for _, v := range s.AdditionalItems {
		v.setParent(s)
		v.applyParentSchema()
	}
	for _, v := range s.Items {
		v.setParent(s)
		v.applyParentSchema()
	}

	for _, v := range s.properties {
		v.setParent(s)
		v.applyParentSchema()
	}

	for _, v := range s.AllOf {
		v.setParent(s)
		v.applyParentSchema()
	}

	for _, v := range s.AnyOf {
		v.setParent(s)
		v.applyParentSchema()
	}

	for _, v := range s.OneOf {
		v.setParent(s)
		v.applyParentSchema()
	}

	if v := s.Not; v != nil {
		v.setParent(s)
		v.applyParentSchema()
	}
}

func (s Schema) BaseURL() *url.URL {
	scope := s.Scope()
	u, err := url.Parse(scope)
	if err != nil {
		// XXX hmm, not sure what to do here
		u = &url.URL{}
	}

	return u
}

func (s *Schema) Root() *Schema {
	if s.parent == nil {
		if pdebug.Enabled {
			pdebug.Printf("Schema %p is root", s)
		}
		return s
	}

	return s.parent.Root()
}

func (s *Schema) findSchemaByID(id string) (*Schema, error) {
	if s.id == id {
		return s, nil
	}

	// XXX Quite unimplemented
	return nil, ErrSchemaNotFound
}

func (s *Schema) ResolveID(id string) (r *Schema, err error) {
	if pdebug.Enabled {
		g := pdebug.IPrintf("START Schema.ResolveID '%s'", id)
		defer func() {
			if err != nil {
				g.IRelease("END Schema.ResolveID '%s': error %s", id, err)
			} else {
				g.IRelease("END Schema.ResolveID '%s' -> %p", id, r)
			}
		}()
	}
	root := s.Root()

	var ok bool
	r, ok = root.schemaByID[id]
	if ok {
		return
	}

	r, err = root.findSchemaByID(id)
	if err != nil {
		return
	}

	root.schemaByID[id] = r
	return
}

func (s Schema) ResolveURL(v string) (u *url.URL, err error) {
	if pdebug.Enabled {
		g := pdebug.IPrintf("START Schema.ResolveURL '%s'", v)
		defer func() {
			if err != nil {
				g.IRelease("END Schema.ResolveURL '%s': error %s", v, err)
			} else {
				g.IRelease("END Schema.ResolveURL '%s' -> '%s'", v, u)
			}
		}()
	}
	base := s.BaseURL()
	if pdebug.Enabled {
		pdebug.Printf("Using base URL '%s'", base)
	}
	u, err = base.Parse(v)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Schema) ResolveReference(v string) (r interface{}, err error) {
	if pdebug.Enabled {
		g := pdebug.IPrintf("START Schema.ResolveReference '%s'", v)
		defer func() {
			if err != nil {
				g.IRelease("END Schema.ResolveReference '%s': error %s", v, err)
			} else {
				g.IRelease("END Schema.ResolveReference '%s'", v)
			}
		}()
	}
	u, err := s.ResolveURL(v)
	if err != nil {
		return nil, err
	}

	var ok bool
	root := s.Root()
	r, ok = root.cachedReference[u.String()]
	if ok {
		pdebug.Printf("s.ResolveReference: Cache HIT for '%s'", u)
		return
	}

	var p *jspointer.JSPointer
	p, err = jspointer.New(u.Fragment)
	if err != nil {
		return
	}

	var t *Schema
	t, err = s.ResolveID(s.Scope())
	if err != nil {
		return
	}

	r, err = p.Get(t)
	if err != nil {
		return nil, err
	}
	s.cachedReference[u.String()] = r

	if pdebug.Enabled {
		pdebug.Printf("s.ResolveReference: Resolved %s (%s)", v, u.Fragment)
	}
	return
}

// Resolve the current schema reference, if '$ref' exists
func (s *Schema) resolveCurrentSchemaReference() (*Schema, error) {
	if s.Reference == "" {
		return s, nil
	}
	thing, err := s.ResolveReference(s.Reference)
	if err != nil {
		return nil, ErrInvalidReference{Reference: s.Reference, Message: err.Error()}
	}

	ref, ok := thing.(*Schema)
	if !ok {
		return nil, ErrInvalidReference{Reference: s.Reference, Message: "returned element is not a Schema"}
	}

	return ref, nil
}

func (s Schema) Validate(v interface{}) error {
	if pdebug.Enabled {
		g := pdebug.IPrintf("START Schema.Validate")
		defer g.IRelease("END Schema.Validate")

		buf, _ := json.MarshalIndent(s, "", "  ")
		pdebug.Printf("schema to validate against: %s", buf)
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	if err := validate(rv, &s); err != nil {
		return err
	}

	return nil
}

func (s Schema) isPropRequired(pname string) bool {
	for _, name := range s.Required {
		if name == pname {
			return true
		}
	}
	return false
}

// getProps return all of the property names for this object.
// XXX Map keys can be something other than strings, but
// we can't really allow it?
func getPropNames(rv reflect.Value) ([]string, error) {
	var keys []string
	switch rv.Kind() {
	case reflect.Map:
		vk := rv.MapKeys()
		keys = make([]string, len(vk))
		for i, v := range vk {
			if v.Kind() != reflect.String {
				return nil, errors.New("panic: can only handle maps with string keys")
			}
			keys[i] = v.String()
		}
	case reflect.Struct:
		if keys = structinfo.JSONFieldsFromStruct(rv); keys == nil {
			// Can't happen, because we check for reflect.Struct,
			// but for completeness
			return nil, errors.New("panic: can only handle structs")
		}
	default:
		return nil, errors.New("cannot get property names from this value")
	}

	return keys, nil
}

func getProp(rv reflect.Value, pname string) reflect.Value {
	switch rv.Kind() {
	case reflect.Map:
		pv := reflect.ValueOf(pname)
		return rv.MapIndex(pv)
	case reflect.Struct:
		i := structinfo.StructFieldFromJSONName(rv, pname)
		if i < 0 {
			return zeroval
		}

		return rv.Field(i)
	default:
		return zeroval
	}
}

func matchType(t PrimitiveType, list PrimitiveTypes) error {
	if len(list) == 0 {
		return nil
	}

	for _, tp := range list {
		switch tp {
		case t:
		default:
			return ErrInvalidType
		}
	}
	if pdebug.Enabled {
		pdebug.Printf("Type match succeeded")
	}
	return nil
}

func validateProp(c reflect.Value, pname string, def *Schema, required bool) (err error) {
	if pdebug.Enabled {
		g := pdebug.IPrintf("START validateProp '%s'", pname)
		defer g.IRelease("END validateProp '%s'", pname)
	}

	def, err = def.resolveCurrentSchemaReference()
	if err != nil {
		return
	}
	pv := getProp(c, pname)
	if pv.Kind() == reflect.Interface {
		pv = pv.Elem()
	}

	if pv == zeroval {
		// no prop by name of pname. is this required?
		if required {
			if pdebug.Enabled {
				pdebug.Printf("Property %s is required, but not found", pname)
			}
			err = ErrRequiredField{Name: pname}
		}
		return
	}

	if err = validate(pv, def); err != nil {
		return
	}
	return
}

// stolen from src/net/dnsclient.go
func isDomainName(s string) bool {
	// See RFC 1035, RFC 3696.
	if len(s) == 0 {
		return false
	}
	if len(s) > 255 {
		return false
	}

	last := byte('.')
	ok := false // Ok once we've seen a letter.
	partlen := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		default:
			return false
		case 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '_':
			ok = true
			partlen++
		case '0' <= c && c <= '9':
			// fine
			partlen++
		case c == '-':
			// Byte before dash cannot be dot.
			if last == '.' {
				return false
			}
			partlen++
		case c == '.':
			// Byte before dot cannot be dot, dash.
			if last == '.' || last == '-' {
				return false
			}
			if partlen > 63 || partlen == 0 {
				return false
			}
			partlen = 0
		}
		last = c
	}
	if last == '-' || partlen > 63 {
		return false
	}

	return ok
}

// Assumes rv is a string (Kind == String)
func validateString(rv reflect.Value, def *Schema) (err error) {
	if pdebug.Enabled {
		g := pdebug.IPrintf("START validateString")
		defer func() {
			if err != nil {
				g.IRelease("END validateString: err = %s", err)
			} else {
				g.IRelease("END validateString (PASS)")
			}
		}()
	}

	if def.MinLength.Initialized {
		if v := def.MinLength.Val; rv.Len() < v {
			err = ErrMinLengthValidationFailed{Len: rv.Len(), MinLength: v}
			return
		}
	}

	if def.MaxLength.Initialized {
		if v := def.MaxLength.Val; rv.Len() > v {
			err = ErrMaxLengthValidationFailed{Len: rv.Len(), MaxLength: v}
			return
		}
	}

	if def.Pattern != nil {
		if !def.Pattern.MatchString(rv.String()) {
			err = ErrPatternValidationFailed{Str: rv.String(), Pattern: def.Pattern}
			return
		}
	}

	if def.Format != "" {
		s := rv.String()
		switch def.Format {
		case FormatDateTime:
			if _, err = time.Parse(time.RFC3339, s); err != nil {
				return
			}
		case FormatEmail:
			if _, err = mail.ParseAddress(s); err != nil {
				return
			}
		case FormatHostname:
			if !isDomainName(s) {
				err = ErrInvalidHostname
				return
			}
		case FormatIPv4:
			// Should only contain numbers and "."
			for _, r := range s {
				switch {
				case r == 0x2E || 0x30 <= r && r <= 0x39:
				default:
					err = ErrInvalidIPv4
					return
				}
			}
			if addr := net.ParseIP(s); addr == nil {
				err = ErrInvalidIPv4
			}
		case FormatIPv6:
			// Should only contain numbers and ":"
			for _, r := range s {
				switch {
				case r == 0x3A || 0x30 <= r && r <= 0x39:
				default:
					err = ErrInvalidIPv6
					return
				}
			}
			if addr := net.ParseIP(s); addr == nil {
				err = ErrInvalidIPv6
			}
		case FormatURI:
			if _, err = url.Parse(s); err != nil {
				return
			}
		default:
			err = ErrInvalidFormat
			return
		}
	}

	return nil
}

func validateNumber(rv reflect.Value, def *Schema) (err error) {
	if pdebug.Enabled {
		g := pdebug.IPrintf("START validateNumber")
		defer func() {
			if err != nil {
				g.IRelease("END validateNumber: err = %s", err)
			} else {
				g.IRelease("END validateNumber (PASS)")
			}
		}()
	}

	var f float64
	// Force value to be float64 so that it's easier to handle
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		f = float64(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		f = float64(rv.Uint())
	case reflect.Float32, reflect.Float64:
		f = rv.Float()
	}

	if def.Minimum.Initialized {
		if def.ExclusiveMinimum.Bool() {
			if f < def.Minimum.Val {
				err = ErrMinimumValidationFailed{Num: f, Min: def.Minimum.Val, Exclusive: true}
				return
			}
		} else {
			if f <= def.Minimum.Val {
				err = ErrMinimumValidationFailed{Num: f, Min: def.Minimum.Val, Exclusive: false}
				return
			}
		}
	}

	if def.Maximum.Initialized {
		if def.ExclusiveMaximum.Bool() {
			if f > def.Maximum.Val {
				err = ErrMaximumValidationFailed{Num: f, Max: def.Maximum.Val, Exclusive: true}
				return
			}
		} else {
			if f >= def.Maximum.Val {
				err = ErrMaximumValidationFailed{Num: f, Max: def.Maximum.Val, Exclusive: false}
				return
			}
		}
	}

	if v := def.MultipleOf.Val; v != 0 {
		var mod float64
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			mod = math.Mod(f, def.MultipleOf.Val)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			mod = math.Mod(f, def.MultipleOf.Val)
		case reflect.Float32, reflect.Float64:
			mod = math.Mod(f, def.MultipleOf.Val)
		}
		if mod != 0 {
			err = ErrMultipleOfValidationFailed
			return
		}
	}
	return nil
}

func validateObject(rv reflect.Value, def *Schema) error {
	names, err := getPropNames(rv)
	if err != nil {
		return err
	}

	if def.MinProperties.Initialized || def.MaxProperties.Initialized {
		// Need to count... 
		count := 0
		for _, name := range names {
			if pv := getProp(rv, name); pv != zeroval {
				count++
			}
		}
		if def.MinProperties.Initialized {
			if v := def.MinProperties.Val; v > count {
				return ErrMinPropertiesValidationFailed{Num: count, Min: v}
			}
		}
		if def.MaxProperties.Initialized {
			if v := def.MaxProperties.Val; v < count {
				return ErrMaxPropertiesValidationFailed{Num: count, Max: v}
			}
		}
	}

	// Make it into a map so we don't check it multiple times
	namesMap := make(map[string]struct{})
	for _, name := range names {
		namesMap[name] = struct{}{}
	}

	for pname, pdef := range def.properties {
		delete(namesMap, pname)
		if err := validateProp(rv, pname, pdef, def.isPropRequired(pname)); err != nil {
			return err
		}
	}

	if pp := def.PatternProperties; len(pp) > 0 {
		for pname := range namesMap {
			for pat, pdef := range pp {
				if pat.MatchString(pname) {
					delete(namesMap, pname)
					if err := validateProp(rv, pname, pdef, def.isPropRequired(pname)); err != nil {
						return err
					}
				}
			}
		}
	}

	if def.AdditionalProperties == nil {
		if len(namesMap) > 0 {
			return ErrAdditionalProperties
		}
	}

	return nil
}

func validate(rv reflect.Value, def *Schema) (err error) {
	if pdebug.Enabled {
		g := pdebug.IPrintf("START validate")
		defer func() {
			if err != nil {
				g.IRelease("END validate: err = %s", err)
			} else {
				g.IRelease("END validate (PASS)")
			}
		}()
	}

	def, err = def.resolveCurrentSchemaReference()
	if err != nil {
		return
	}

	switch {
	case def.Not != nil:
		if pdebug.Enabled {
			pdebug.Printf("Checking 'not' constraint")
		}

		// Everything is peachy, if errors do occur
		if err2 := validate(rv, def.Not); err2 == nil {
			err = ErrNotValidationFailed
			return
		}
	case len(def.AllOf) > 0:
		if pdebug.Enabled {
			pdebug.Printf("Checking 'allOf' constraint")
		}
		for _, s1 := range def.AllOf {
			if err = validate(rv, s1); err != nil {
				return
			}
		}
	case len(def.AnyOf) > 0:
		if pdebug.Enabled {
			pdebug.Printf("Checking 'anyOf' constraint")
		}
		ok := false
		for _, s1 := range def.AnyOf {
			// don't use err from upper scope
			if err := validate(rv, s1); err == nil {
				ok = true
				break
			}
		}
		if !ok {
			err = ErrAnyOfValidationFailed
			return
		}
	case len(def.OneOf) > 0:
		if pdebug.Enabled {
			pdebug.Printf("Checking 'oneOf' constraint")
		}
		count := 0
		for _, s1 := range def.OneOf {
			// don't use err from upper scope
			if err := validate(rv, s1); err == nil {
				count++
			}
		}
		if count != 1 {
			err = ErrOneOfValidationFailed
			return
		}
	}

	switch rv.Kind() {
	case reflect.Map, reflect.Struct:
		if err = matchType(ObjectType, def.Type); err != nil {
			return
		}
		if err = validateObject(rv, def); err != nil {
			return
		}
	case reflect.String:
		// Make sure string type is allowed here
		if err = matchType(StringType, def.Type); err != nil {
			return
		}
		if err = validateString(rv, def); err != nil {
			return
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64:
		typeOK := false
		intOK := true
		if err = matchType(IntegerType, def.Type); err == nil {
			// Check if this is a valid integer
			if f := rv.Float(); math.Floor(f) == f {
				// it's valid, bail out of this type checking, because we're all good
				typeOK = true
				goto TYPECHECK_DONE
			}
			intOK = false
		}

		if err = matchType(NumberType, def.Type); err != nil {
			return
		}
		typeOK = true
	TYPECHECK_DONE:
		if !typeOK {
			if !intOK {
				err = ErrIntegerValidationFailed
			} else {
				err = ErrNumberValidationFailed
			}
			return
		}

		if err = validateNumber(rv, def); err != nil {
			return
		}
	default:
		if pdebug.Enabled {
			pdebug.Printf("object type is invalid: %s", rv.Kind())
		}
		err = ErrInvalidType
		return
	}
	return nil
}

func (s Schema) Scope() string {
	if s.id != "" || s.parent == nil {
		return s.id
	}

	return s.parent.Scope()
}

func (s Schema) MaxItems() int {
	return s.maxItems.Val
}

func (s Schema) MinItems() int {
	return s.minItems.Val
}

func (s Schema) Properties() []string {
	l := make([]string, 0, len(s.properties))
	for k := range s.properties {
		l = append(l, k)
	}
	return l
}

func extractNumber(n *Number, m map[string]interface{}, s string) error {
	v, ok := m[s]
	if !ok {
		return nil
	}

	switch v.(type) {
	case float64:
	default:
		return ErrInvalidFieldValue{Name: s}
	}

	n.Val = v.(float64)
	n.Initialized = true
	return nil
}

func extractInt(n *Integer, m map[string]interface{}, s string) error {
	v, ok := m[s]
	if !ok {
		return nil
	}

	switch v.(type) {
	case float64:
		n.Val = int(v.(float64))
		n.Initialized = true
	default:
		return ErrInvalidFieldValue{Name: s}
	}

	return nil
}

func extractBool(b *Bool, m map[string]interface{}, s string, def bool) error {
	b.Default = def
	v, ok := m[s]
	if !ok {
		return nil
	}

	switch v.(type) {
	case bool:
	default:
		return ErrInvalidFieldValue{Name: s}
	}

	b.Val = v.(bool)
	b.Initialized = true
	return nil
}

func extractString(m map[string]interface{}, s string) (string, error) {
	if v, ok := m[s]; ok {
		switch v.(type) {
		case string:
			return v.(string), nil
		default:
			return "", ErrInvalidFieldValue{Name: s}
		}
	}

	return "", nil
}

func extractStringList(m map[string]interface{}, s string) ([]string, error) {
	if v, ok := m[s]; ok {
		switch v.(type) {
		case string:
			return []string{v.(string)}, nil
		case []interface{}:
			l := v.([]interface{})
			r := make([]string, len(l))
			for i, x := range l {
				switch x.(type) {
				case string:
					r[i] = x.(string)
				default:
					return nil, ErrInvalidFieldValue{Name: s}
				}
			}
			return r, nil
		default:
			return nil, ErrInvalidFieldValue{Name: s}
		}
	}

	return nil, nil
}

func extractFormat(m map[string]interface{}, s string) (Format, error) {
	v, err := extractString(m, s)
	if err != nil {
		return "", err
	}
	return Format(v), nil
}

func extractJSPointer(m map[string]interface{}, s string) (string, error) {
	v, err := extractString(m, s)
	if err != nil {
		return "", err
	}

	return v, nil
}

func extractInterface(m map[string]interface{}, s string) (interface{}, error) {
	if v, ok := m[s]; ok {
		return v, nil
	}
	return nil, nil
}

func extractInterfaceList(m map[string]interface{}, s string) ([]interface{}, error) {
	if v, ok := m[s]; ok {
		switch v.(type) {
		case []interface{}:
			return v.([]interface{}), nil
		default:
			return nil, ErrInvalidFieldValue{Name: s}
		}
	}

	return nil, nil
}

func extractRegexp(m map[string]interface{}, s string) (*regexp.Regexp, error) {
	if v, ok := m[s]; ok {
		switch v.(type) {
		case string:
			return regexp.Compile(v.(string))
		default:
			return nil, ErrInvalidType
		}
	}
	return nil, nil
}

func extractSchema(m map[string]interface{}, name string) (*Schema, error) {
	if v, ok := m[name]; ok {
		switch v.(type) {
		case map[string]interface{}:
		default:
			return nil, ErrInvalidType
		}
		s := New()
		if err := s.extract(v.(map[string]interface{})); err != nil {
			return nil, err
		}
		return s, nil
	}
	return nil, nil
}

func extractSchemaList(m map[string]interface{}, name string) ([]*Schema, error) {
	if v, ok := m[name]; ok {
		switch v.(type) {
		case []interface{}:
			l := v.([]interface{})
			r := make([]*Schema, len(l))
			for i, d := range l {
				s := New()
				if err := s.extract(d.(map[string]interface{})); err != nil {
					return nil, err
				}
				r[i] = s
			}
			return r, nil
		case map[string]interface{}:
			s := New()
			if err := s.extract(v.(map[string]interface{})); err != nil {
				return nil, err
			}
			return []*Schema{s}, nil
		default:
			return nil, ErrInvalidFieldValue{Name: name}
		}
	}

	return nil, nil
}

func extractSchemaMap(m map[string]interface{}, name string) (map[string]*Schema, error) {
	if v, ok := m[name]; ok {
		switch v.(type) {
		case map[string]interface{}:
		default:
			return nil, ErrInvalidFieldValue{Name: name}
		}

		r := make(map[string]*Schema)
		for k, data := range v.(map[string]interface{}) {
			// data better be a map
			switch data.(type) {
			case map[string]interface{}:
			default:
				return nil, ErrInvalidFieldValue{Name: name}
			}
			s := New()
			if err := s.extract(data.(map[string]interface{})); err != nil {
				return nil, err
			}
			r[k] = s
		}
		return r, nil
	}
	return nil, nil
}

func extractRegexpToSchemaMap(m map[string]interface{}, name string) (map[*regexp.Regexp]*Schema, error) {
	if v, ok := m[name]; ok {
		switch v.(type) {
		case map[string]interface{}:
		default:
			return nil, ErrInvalidFieldValue{Name: name}
		}

		r := make(map[*regexp.Regexp]*Schema)
		for k, data := range v.(map[string]interface{}) {
			// data better be a map
			switch data.(type) {
			case map[string]interface{}:
			default:
				return nil, ErrInvalidFieldValue{Name: name}
			}
			s := New()
			if err := s.extract(data.(map[string]interface{})); err != nil {
				return nil, err
			}

			rx, err := regexp.Compile(k)
			if err != nil {
				return nil, err
			}

			r[rx] = s
		}
		return r, nil
	}
	return nil, nil
}

func (s *Schema) UnmarshalJSON(data []byte) error {
	m := map[string]interface{}{}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}

	return s.extract(m)
}

func (s *Schema) extract(m map[string]interface{}) error {
	var err error

	if s.id, err = extractString(m, "id"); err != nil {
		return err
	}

	if s.Title, err = extractString(m, "title"); err != nil {
		return err
	}

	if s.Description, err = extractString(m, "description"); err != nil {
		return err
	}

	if s.Required, err = extractStringList(m, "required"); err != nil {
		return err
	}

	if s.SchemaRef, err = extractJSPointer(m, "$schema"); err != nil {
		return err
	}

	if s.Reference, err = extractJSPointer(m, "$ref"); err != nil {
		return err
	}

	if s.Format, err = extractFormat(m, "format"); err != nil {
		return err
	}

	if s.Enum, err = extractInterfaceList(m, "enum"); err != nil {
		return err
	}

	if s.Default, err = extractInterface(m, "default"); err != nil {
		return err
	}

	if v, ok := m["type"]; ok {
		switch v.(type) {
		case string:
			t, err := primitiveFromString(v.(string))
			if err != nil {
				return err
			}
			s.Type = PrimitiveTypes{t}
		case []string:
			l := v.([]string)
			s.Type = make(PrimitiveTypes, len(l))
			for i, ts := range l {
				t, err := primitiveFromString(ts)
				if err != nil {
					return err
				}
				s.Type[i] = t
			}
		default:
			return ErrInvalidFieldValue{Name: "type"}
		}
	}

	if s.Definitions, err = extractSchemaMap(m, "definitions"); err != nil {
		return err
	}

	if s.Items, err = extractSchemaList(m, "items"); err != nil {
		return err
	}

	if s.Pattern, err = extractRegexp(m, "pattern"); err != nil {
		return err
	}

	if extractInt(&s.MinLength, m, "minLength"); err != nil {
		return err
	}

	if extractInt(&s.MaxLength, m, "maxLength"); err != nil {
		return err
	}

	if extractInt(&s.minItems, m, "minItems"); err != nil {
		return err
	}

	if extractInt(&s.maxItems, m, "maxItems"); err != nil {
		return err
	}

	if err = extractBool(&s.UniqueItems, m, "uniqueItems", false); err != nil {
		return err
	}

	if err = extractInt(&s.MaxProperties, m, "maxProperties"); err != nil {
		return err
	}

	if err = extractInt(&s.MinProperties, m, "minProperties"); err != nil {
		return err
	}

	if err = extractNumber(&s.Minimum, m, "minimum"); err != nil {
		return err
	}

	if err = extractBool(&s.ExclusiveMinimum, m, "exclusiveminimum", false); err != nil {
		return err
	}

	if err = extractNumber(&s.Maximum, m, "maximum"); err != nil {
		return err
	}

	if err = extractBool(&s.ExclusiveMaximum, m, "exclusivemaximum", false); err != nil {
		return err
	}

	if err = extractNumber(&s.MultipleOf, m, "multipleOf"); err != nil {
		return err
	}

	if s.properties, err = extractSchemaMap(m, "properties"); err != nil {
		return err
	}

	if _, ok := m["additionalProperties"]; !ok {
		// doesn't exist. it's an empty schema
		s.AdditionalProperties = &AdditionalProperties{}
	} else {
		var b Bool
		if err = extractBool(&b, m, "additionalProperties", true); err == nil {
			if b.Bool() {
				s.AdditionalProperties = &AdditionalProperties{}
			} else {
			}
		} else {
			// Oh, it's not a boolean?
			var apSchema *Schema
			if apSchema, err = extractSchema(m, "additionalProperties"); err != nil {
				return err
			}
			s.AdditionalProperties = &AdditionalProperties{apSchema}
		}
	}

	if s.PatternProperties, err = extractRegexpToSchemaMap(m, "patternProperties"); err != nil {
		return err
	}

	if s.properties, err = extractSchemaMap(m, "properties"); err != nil {
		return err
	}

	if s.AllOf, err = extractSchemaList(m, "allOf"); err != nil {
		return err
	}

	if s.AnyOf, err = extractSchemaList(m, "anyOf"); err != nil {
		return err
	}

	if s.OneOf, err = extractSchemaList(m, "oneOf"); err != nil {
		return err
	}

	if s.Not, err = extractSchema(m, "not"); err != nil {
		return err
	}

	s.applyParentSchema()

	return nil
}

func place(m map[string]interface{}, name string, v interface{}) {
	m[name] = v
}

func placeString(m map[string]interface{}, name, s string) {
	if s != "" {
		place(m, name, s)
	}
}

func placeList(m map[string]interface{}, name string, l []interface{}) {
	if len(l) > 0 {
		place(m, name, l)
	}
}
func placeSchemaList(m map[string]interface{}, name string, l []*Schema) {
	if len(l) > 0 {
		place(m, name, l)
	}
}

func placeSchemaMap(m map[string]interface{}, name string, l map[string]*Schema) {
	if len(l) > 0 {
		defs := make(map[string]*Schema)
		place(m, name, defs)

		for k, v := range l {
			defs[k] = v
		}
	}
}

func placeStringList(m map[string]interface{}, name string, l []string) {
	if len(l) > 0 {
		place(m, name, l)
	}
}

func placeBool(m map[string]interface{}, name string, value Bool) {
	place(m, name, value.Bool())
}

func placeNumber(m map[string]interface{}, name string, n Number) {
	if !n.Initialized {
		return
	}
	place(m, name, n.Val)
}

func placeInteger(m map[string]interface{}, name string, n Integer) {
	if !n.Initialized {
		return
	}
	place(m, name, n.Val)
}

func (s Schema) MarshalJSON() ([]byte, error) {
	m := make(map[string]interface{})

	placeString(m, "id", s.id)
	placeString(m, "title", s.Title)
	placeString(m, "description", s.Description)
	placeString(m, "$schema", s.SchemaRef)
	placeString(m, "$ref", s.Reference)
	placeStringList(m, "required", s.Required)
	placeList(m, "enum", s.Enum)
	switch len(s.Type) {
	case 0:
	case 1:
		m["type"] = s.Type[0]
	default:
		m["type"] = s.Type
	}

	if s.AllowAdditionalItems {
		m["additionalItems"] = true
	}

	if rx := s.Pattern; rx != nil {
		placeString(m, "pattern", rx.String())
	}
	placeInteger(m, "maxLength", s.MaxLength)
	placeInteger(m, "minLength", s.MinLength)
	placeInteger(m, "maxItems", s.maxItems)
	placeInteger(m, "minItems", s.minItems)
	placeInteger(m, "maxProperties", s.MaxProperties)
	placeInteger(m, "minProperties", s.MinProperties)
	if s.UniqueItems.Initialized {
		placeBool(m, "uniqueItems", s.UniqueItems)
	}
	placeSchemaMap(m, "definitions", s.Definitions)

	switch len(s.Items) {
	case 0: // do nothing
	case 1:
		m["items"] = s.Items[0]
	case 2:
		m["items"] = s.Items
	}

	placeSchemaMap(m, "properties", s.properties)
	if len(s.PatternProperties) > 0 {
		rxm := make(map[string]*Schema)
		for rx, rxs := range s.PatternProperties {
			rxm[rx.String()] = rxs
		}
		placeSchemaMap(m, "patternProperties", rxm)
	}

	placeSchemaList(m, "allOf", s.AllOf)
	placeSchemaList(m, "anyOf", s.AnyOf)
	placeSchemaList(m, "oneOf", s.OneOf)

	if s.Default != nil {
		m["default"] = s.Default
	}

	placeString(m, "format", string(s.Format))
	placeNumber(m, "minimum", s.Minimum)
	if s.ExclusiveMinimum.Initialized {
		placeBool(m, "exclusiveMinimum", s.ExclusiveMinimum)
	}
	placeNumber(m, "maximum", s.Maximum)
	if s.ExclusiveMaximum.Initialized {
		placeBool(m, "exclusiveMaximum", s.ExclusiveMaximum)
	}

	if ap := s.AdditionalProperties; ap != nil {
		if ap.Schema != nil {
			place(m, "additionalProperties", ap.Schema)
		}
	} else {
		// additionalProperties: false
		placeBool(m, "additionalProperties", Bool{Val: false, Initialized: true})
	}

	if s.MultipleOf.Val != 0 {
		placeNumber(m, "multipleOf", s.MultipleOf)
	}

	if v := s.Not; v != nil {
		place(m, "not", v)
	}

	return json.Marshal(m)
}
