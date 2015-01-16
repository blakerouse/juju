// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package google

import (
	"path"

	"code.google.com/p/google-api-go-client/compute/v1"
)

// AvailabilityZone represents a single GCE zone. It satisfies the
// {provider/common}.AvailabilityZone interface.
type AvailabilityZone struct {
	zone *compute.Zone
}

// NewZone build an availability zone from the provided name and status
// and returns it.
func NewZone(name, status string) AvailabilityZone {
	return AvailabilityZone{zone: &compute.Zone{
		Name:   name,
		Status: status,
	}}
}

// TODO(ericsnow) Add a Region getter?

// Name returns the zone's name.
func (z AvailabilityZone) Name() string {
	return z.zone.Name
}

// Status returns the status string for the zone. It will match one of
// the Status* constants defined in the package.
func (z AvailabilityZone) Status() string {
	return z.zone.Status
}

// Available returns whether or not the zone is available for provisioning.
func (z AvailabilityZone) Available() bool {
	// https://cloud.google.com/compute/docs/reference/latest/zones#status
	return z.Status() == StatusUp
}

// zoneName returns the unqualified name of the corresponding zone
// for some of the low-level types that have that information. If value
// does not have the information or is of an unsupported type then the
// empty string is returned.
func zoneName(value interface{}) string {
	// We trust that path.Base will always give the right answer
	// when used.
	switch typed := value.(type) {
	case *compute.Instance:
		return path.Base(typed.Zone)
	case *compute.Operation:
		return path.Base(typed.Zone)
	case *Instance:
		return path.Base(typed.InstanceSummary.ZoneName)
	default:
		// TODO(ericsnow) Fail?
		return ""
	}
}
