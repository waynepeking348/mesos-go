package mesos

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/gogo/protobuf/proto"
)

type (
	Resources         []Resource
	resourceErrorType int

	resourceError struct {
		errorType resourceErrorType
		reason    string
		spec      Resource
	}
)

const (
	resourceErrorTypeIllegalName resourceErrorType = iota
	resourceErrorTypeIllegalType
	resourceErrorTypeUnsupportedType
	resourceErrorTypeIllegalScalar
	resourceErrorTypeIllegalRanges
	resourceErrorTypeIllegalSet
	resourceErrorTypeIllegalDisk
	resourceErrorTypeIllegalReservation

	noReason = "" // make error generation code more readable
)

var (
	resourceErrorMessages = map[resourceErrorType]string{
		resourceErrorTypeIllegalName:        "missing or illegal resource name",
		resourceErrorTypeIllegalType:        "missing or illegal resource type",
		resourceErrorTypeUnsupportedType:    "unsupported resource type",
		resourceErrorTypeIllegalScalar:      "illegal scalar resource",
		resourceErrorTypeIllegalRanges:      "illegal ranges resource",
		resourceErrorTypeIllegalSet:         "illegal set resource",
		resourceErrorTypeIllegalDisk:        "illegal disk resource",
		resourceErrorTypeIllegalReservation: "illegal resource reservation",
	}
)

func (t resourceErrorType) Generate(reason string) error {
	msg := resourceErrorMessages[t]
	if reason != noReason {
		if msg != "" {
			msg += ": " + reason
		} else {
			msg = reason
		}
	}
	return &resourceError{errorType: t, reason: msg}
}

func (err *resourceError) Reason() string          { return err.reason }
func (err *resourceError) Resource() Resource      { return err.spec }
func (err *resourceError) WithResource(r Resource) { err.spec = r }

func (err *resourceError) Error() string {
	// TODO(jdef) include additional context here? (type, resource)
	if err.reason != "" {
		return "resource error: " + err.reason
	}
	return "resource error"
}

func IsResourceError(err error) (ok bool) {
	_, ok = err.(*resourceError)
	return
}

func (r *Resource_ReservationInfo) Assign() func(interface{}) {
	return func(v interface{}) {
		type reserver interface {
			WithReservation(*Resource_ReservationInfo)
		}
		if ri, ok := v.(reserver); ok {
			ri.WithReservation(r)
		}
	}
}

func (resources Resources) Clone() Resources {
	if resources == nil {
		return nil
	}
	clone := make(Resources, 0, len(resources))
	for i := range resources {
		rr := proto.Clone(&resources[i]).(*Resource)
		clone = append(clone, *rr)
	}
	return clone
}

// Minus calculates and returns the result of `resources - that` without modifying either
// the receiving `resources` or `that`.
func (resources Resources) Minus(that ...Resource) Resources {
	x := resources.Clone()
	return x.Subtract(that...)
}

// Subtract subtracts `that` from the receiving `resources` and returns the result (the modified
// `resources` receiver).
func (resources *Resources) Subtract(that ...Resource) (rs Resources) {
	if resources != nil {
		if len(that) > 0 {
			x := make(Resources, len(that))
			copy(x, that)
			that = x

			for i := range that {
				resources.Subtract1(that[i])
			}
		}
		rs = *resources
	}
	return
}

// Plus calculates and returns the result of `resources + that` without modifying either
// the receiving `resources` or `that`.
func (resources Resources) Plus(that ...Resource) Resources {
	x := resources.Clone()
	return x.Add(that...)
}

// Add adds `that` to the receiving `resources` and returns the result (the modified
// `resources` receiver).
func (resources *Resources) Add(that ...Resource) (rs Resources) {
	if resources != nil {
		rs = *resources
	}
	for i := range that {
		rs = rs._add(that[i])
	}
	if resources != nil {
		*resources = rs
	}
	return
}

// Add1 adds `that` to the receiving `resources` and returns the result (the modified
// `resources` receiver).
func (resources *Resources) Add1(that Resource) (rs Resources) {
	if resources != nil {
		rs = *resources
	}
	rs = rs._add(that)
	if resources != nil {
		*resources = rs
	}
	return
}

func (resources Resources) _add(that Resource) Resources {
	if that.Validate() != nil || that.IsEmpty() {
		return resources
	}
	for i := range resources {
		r := &resources[i]
		if r.Addable(that) {
			r.Add(that)
			return resources
		}
	}
	// cannot be combined with an existing resource
	r := proto.Clone(&that).(*Resource)
	return append(resources, *r)
}

// Minus1 calculates and returns the result of `resources - that` without modifying either
// the receiving `resources` or `that`.
func (resources *Resources) Minus1(that Resource) Resources {
	x := resources.Clone()
	return x.Subtract1(that)
}

// Subtract1 subtracts `that` from the receiving `resources` and returns the result (the modified
// `resources` receiver).
func (resources *Resources) Subtract1(that Resource) Resources {
	if resources == nil {
		return nil
	}
	if that.Validate() == nil && !that.IsEmpty() {
		for i := range *resources {
			r := &(*resources)[i]
			if r.Subtractable(that) {
				r.Subtract(that)
				// remove the resource if it becomes invalid or zero.
				// need to do validation in order to strip negative scalar
				// resource objects.
				if r.Validate() != nil || r.IsEmpty() {
					// delete resource at i, without leaking an uncollectable Resource
					// a, a[len(a)-1] = append(a[:i], a[i+1:]...), nil
					(*resources), (*resources)[len((*resources))-1] = append((*resources)[:i], (*resources)[i+1:]...), Resource{}
				}
				break
			}
		}
	}
	return *resources
}

func (resources Resources) String() string {
	if len(resources) == 0 {
		return ""
	}
	buf := bytes.Buffer{}
	for i := range resources {
		if i > 0 {
			buf.WriteString(";")
		}
		r := &resources[i]
		buf.WriteString(r.Name)
		buf.WriteString("(")
		buf.WriteString(r.GetRole())
		if ri := r.GetReservation(); ri != nil {
			buf.WriteString(", ")
			buf.WriteString(ri.GetPrincipal())
		}
		buf.WriteString(")")
		if d := r.GetDisk(); d != nil {
			buf.WriteString("[")
			if p := d.GetPersistence(); p != nil {
				buf.WriteString(p.GetID())
			}
			if v := d.GetVolume(); v != nil {
				buf.WriteString(":")
				vconfig := v.GetContainerPath()
				if h := v.GetHostPath(); h != "" {
					vconfig = h + ":" + vconfig
				}
				if m := v.Mode; m != nil {
					switch *m {
					case RO:
						vconfig += ":ro"
					case RW:
						vconfig += ":rw"
					default:
						panic("unrecognized volume mode: " + m.String())
					}
				}
				buf.WriteString(vconfig)
			}
			buf.WriteString("]")
		}
		buf.WriteString(":")
		switch r.GetType() {
		case SCALAR:
			buf.WriteString(strconv.FormatFloat(r.GetScalar().GetValue(), 'f', -1, 64))
		case RANGES:
			buf.WriteString("[")
			ranges := Ranges(r.GetRanges().GetRange())
			for j := range ranges {
				if j > 0 {
					buf.WriteString(",")
				}
				buf.WriteString(strconv.FormatUint(ranges[j].Begin, 10))
				buf.WriteString("-")
				buf.WriteString(strconv.FormatUint(ranges[j].End, 10))
			}
			buf.WriteString("]")
		case SET:
			buf.WriteString("{")
			items := r.GetSet().GetItem()
			for j := range items {
				if j > 0 {
					buf.WriteString(",")
				}
				buf.WriteString(items[j])
			}
			buf.WriteString("}")
		}
	}
	return buf.String()
}

func (left *Resource) Validate() error {
	if left.GetName() == "" {
		return resourceErrorTypeIllegalName.Generate(noReason)
	}
	if _, ok := Value_Type_name[int32(left.GetType())]; !ok {
		return resourceErrorTypeIllegalType.Generate(noReason)
	}
	switch left.GetType() {
	case SCALAR:
		if s := left.GetScalar(); s == nil || left.GetRanges() != nil || left.GetSet() != nil {
			return resourceErrorTypeIllegalScalar.Generate(noReason)
		} else if s.GetValue() < 0 {
			return resourceErrorTypeIllegalScalar.Generate("value < 0")
		}
	case RANGES:
		r := left.GetRanges()
		if left.GetScalar() != nil || r == nil || left.GetSet() != nil {
			return resourceErrorTypeIllegalRanges.Generate(noReason)
		}
		for i, rr := range r.GetRange() {
			// ensure that ranges are not inverted
			if rr.Begin > rr.End {
				return resourceErrorTypeIllegalRanges.Generate("begin > end")
			}
			// ensure that ranges don't overlap (but not necessarily squashed)
			for j := i + 1; j < len(r.GetRange()); j++ {
				r2 := r.GetRange()[j]
				if rr.Begin <= r2.Begin && r2.Begin <= rr.End {
					return resourceErrorTypeIllegalRanges.Generate("overlapping ranges")
				}
			}
		}
	case SET:
		s := left.GetSet()
		if left.GetScalar() != nil || left.GetRanges() != nil || s == nil {
			return resourceErrorTypeIllegalSet.Generate(noReason)
		}
		unique := make(map[string]struct{}, len(s.GetItem()))
		for _, x := range s.GetItem() {
			if _, found := unique[x]; found {
				return resourceErrorTypeIllegalSet.Generate("duplicated elements")
			}
			unique[x] = struct{}{}
		}
	default:
		return resourceErrorTypeUnsupportedType.Generate(noReason)
	}

	// check for disk resource
	if left.GetDisk() != nil && left.GetName() != "disk" {
		return resourceErrorTypeIllegalDisk.Generate("DiskInfo should not be set for \"" + left.GetName() + "\" resource")
	}

	// check for invalid state of (role,reservation) pair
	if left.GetRole() == "*" && left.GetReservation() != nil {
		return resourceErrorTypeIllegalReservation.Generate("default role cannot be dynamically assigned")
	}

	return nil
}

func (r *Resource_ReservationInfo) Equivalent(right *Resource_ReservationInfo) bool {
	if (r == nil) != (right == nil) {
		return false
	}
	return r.GetPrincipal() == right.GetPrincipal()
}

func (left *Resource_DiskInfo) Equivalent(right *Resource_DiskInfo) bool {
	// NOTE: We ignore 'volume' inside DiskInfo when doing comparison
	// because it describes how this resource will be used which has
	// nothing to do with the Resource object itself. A framework can
	// use this resource and specify different 'volume' every time it
	// uses it.
	// see https://github.com/apache/mesos/blob/0.25.0/src/common/resources.cpp#L67
	if (left == nil) != (right == nil) {
		return false
	}
	if a, b := left.GetPersistence(), right.GetPersistence(); (a == nil) != (b == nil) {
		return false
	} else if a != nil {
		return a.GetID() == b.GetID()
	}
	return true
}

// Equivalent returns true if right is equivalent to left (differs from Equal in that
// deeply nested values are test for equivalence, not equality).
func (left *Resource) Equivalent(right Resource) bool {
	if left.GetName() != right.GetName() ||
		left.GetType() != right.GetType() ||
		left.GetRole() != right.GetRole() {
		return false
	}
	if !left.GetReservation().Equivalent(right.GetReservation()) {
		return false
	}
	if !left.GetDisk().Equivalent(right.GetDisk()) {
		return false
	}
	if (left.Revocable == nil) != (right.Revocable == nil) {
		return false
	}

	switch left.GetType() {
	case SCALAR:
		return left.GetScalar().Compare(right.GetScalar()) == 0
	case RANGES:
		return Ranges(left.GetRanges().GetRange()).Equivalent(right.GetRanges().GetRange())
	case SET:
		return left.GetSet().Compare(right.GetSet()) == 0
	default:
		return false
	}
}

// Addable tests if we can add two Resource objects together resulting in one
// valid Resource object. For example, two Resource objects with
// different name, type or role are not addable.
func (left *Resource) Addable(right Resource) bool {
	if left.GetName() != right.GetName() ||
		left.GetType() != right.GetType() ||
		left.GetRole() != right.GetRole() {
		return false
	}
	if !left.GetReservation().Equivalent(right.GetReservation()) {
		return false
	}
	if !left.GetDisk().Equivalent(right.GetDisk()) {
		return false
	}

	// from apache/mesos: src/common/resources.cpp
	// TODO(jieyu): Even if two Resource objects with DiskInfo have the
	// same persistence ID, they cannot be added together. In fact, this
	// shouldn't happen if we do not add resources from different
	// namespaces (e.g., across slave). Consider adding a warning.
	if left.GetDisk().GetPersistence() != nil {
		return false
	}
	if (left.Revocable == nil) != (right.Revocable == nil) {
		return false
	}
	return true
}

// Subtractable tests if we can subtract "right" from "left" resulting in one
// valid Resource object. For example, two Resource objects with different
// name, type or role are not subtractable.
// NOTE: Set subtraction is always well defined, it does not require
// 'right' to be contained within 'left'. For example, assuming that
// "left = {1, 2}" and "right = {2, 3}", "left" and "right" are
// subtractable because "left - right = {1}". However, "left" does not
// contain "right".
func (left *Resource) Subtractable(right Resource) bool {
	if left.GetName() != right.GetName() ||
		left.GetType() != right.GetType() ||
		left.GetRole() != right.GetRole() {
		return false
	}
	if !left.GetReservation().Equivalent(right.GetReservation()) {
		return false
	}
	if !left.GetDisk().Equivalent(right.GetDisk()) {
		return false
	}

	// NOTE: For Resource objects that have DiskInfo, we can only do
	// subtraction if they are **equal**.
	if left.GetDisk().GetPersistence() != nil && !left.Equivalent(right) {
		return false
	}
	if (left.Revocable == nil) != (right.Revocable == nil) {
		return false
	}
	return true
}

// Contains tests if "right" is contained in "left".
func (left Resource) Contains(right Resource) bool {
	if !left.Subtractable(right) {
		return false
	}
	switch left.GetType() {
	case SCALAR:
		return right.GetScalar().Compare(left.GetScalar()) <= 0
	case RANGES:
		return right.GetRanges().Compare(left.GetRanges()) <= 0
	case SET:
		return right.GetSet().Compare(left.GetSet()) <= 0
	default:
		return false
	}
}

// Subtract removes right from left.
// This func panics if the resource types don't match.
func (left *Resource) Subtract(right Resource) {
	switch right.checkType(left.GetType()) {
	case SCALAR:
		left.Scalar = left.GetScalar().Subtract(right.GetScalar())
	case RANGES:
		left.Ranges = left.GetRanges().Subtract(right.GetRanges())
	case SET:
		left.Set = left.GetSet().Subtract(right.GetSet())
	}
}

// Add adds right to left.
// This func panics if the resource types don't match.
func (left *Resource) Add(right Resource) {
	switch right.checkType(left.GetType()) {
	case SCALAR:
		left.Scalar = left.GetScalar().Add(right.GetScalar())
	case RANGES:
		left.Ranges = left.GetRanges().Add(right.GetRanges())
	case SET:
		left.Set = left.GetSet().Add(right.GetSet())
	}
}

// checkType panics if the type of this resources != t
func (left *Resource) checkType(t Value_Type) Value_Type {
	if left != nil && left.GetType() != t {
		panic(fmt.Sprintf("expected type %v instead of %v", t, left.GetType()))
	}
	return t
}

// IsEmpty returns true if the value of this resource is equivalent to the zero-value,
// where a zero-length slice or map is equivalent to a nil reference to such.
func (left *Resource) IsEmpty() bool {
	if left == nil {
		return true
	}
	switch left.GetType() {
	case SCALAR:
		return left.GetScalar().GetValue() == 0
	case RANGES:
		return len(left.GetRanges().GetRange()) == 0
	case SET:
		return len(left.GetSet().GetItem()) == 0
	}
	return false
}

// IsUnreserved returns true if this resource neither statically or dynamically reserved.
// A resource is considered statically reserved if it has a non-default role.
func (left *Resource) IsUnreserved() bool {
	// role != RoleDefault     -> static reservation
	// GetReservation() != nil -> dynamic reservation
	// return {no-static-reservation} && {no-dynamic-reservation}
	return left.GetRole() == "*" && left.GetReservation() == nil
}

// IsReserved returns true if this resource has been reserved for the given role.
// If role=="" then return true if there are no static or dynamic reservations for this resource.
// It's expected that this Resource has already been validated (see Validate).
func (left *Resource) IsReserved(role string) bool {
	if role != "" {
		return !left.IsUnreserved() && role == left.GetRole()
	}
	return !left.IsUnreserved()
}

// IsDynamicallyReserved returns true if this resource has a non-nil reservation descriptor
func (left *Resource) IsDynamicallyReserved() bool {
	return left.GetReservation() != nil
}

// IsRevocable returns true if this resource has a non-nil revocable descriptor
func (left *Resource) IsRevocable() bool {
	return left.GetRevocable() != nil
}

// IsPersistentVolume returns true if this is a disk resource with a non-nil Persistence descriptor
func (left *Resource) IsPersistentVolume() bool {
	return left.GetDisk().GetPersistence() != nil
}
