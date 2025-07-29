// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"fmt"

	committeeservice "github.com/linuxfoundation/lfx-v2-committee-service/gen/committee_service"
	"goa.design/clue/log"
	"goa.design/goa/v3/security"
)

// committee-service service example implementation.
// The example methods log the requests and return zero values.
type committeeServicesrvc struct{}

// NewCommitteeService returns the committee-service service implementation.
func NewCommitteeService() committeeservice.Service {
	return &committeeServicesrvc{}
}

// JWTAuth implements the authorization logic for service "committee-service"
// for the "jwt" security scheme.
func (s *committeeServicesrvc) JWTAuth(ctx context.Context, token string, scheme *security.JWTScheme) (context.Context, error) {
	//
	// TBD: add authorization logic.
	//
	// In case of authorization failure this function should return
	// one of the generated error structs, e.g.:
	//
	//    return ctx, myservice.MakeUnauthorizedError("invalid token")
	//
	// Alternatively this function may return an instance of
	// goa.ServiceError with a Name field value that matches one of
	// the design error names, e.g:
	//
	//    return ctx, goa.PermanentError("unauthorized", "invalid token")
	//
	return ctx, fmt.Errorf("not implemented")
}

// Create Committee
func (s *committeeServicesrvc) CreateCommittee(ctx context.Context, p *committeeservice.CreateCommitteePayload) (res *committeeservice.CommitteeFull, err error) {
	res = &committeeservice.CommitteeFull{}
	log.Printf(ctx, "committeeService.create-committee")
	return
}

// Get Committee
func (s *committeeServicesrvc) GetCommitteeBase(ctx context.Context, p *committeeservice.GetCommitteeBasePayload) (res *committeeservice.GetCommitteeBaseResult, err error) {
	res = &committeeservice.GetCommitteeBaseResult{}
	log.Printf(ctx, "committeeService.get-committee-base")
	return
}

// Update Committee
func (s *committeeServicesrvc) UpdateCommitteeBase(ctx context.Context, p *committeeservice.UpdateCommitteeBasePayload) (res *committeeservice.CommitteeFullWithReadonlyAttributes, err error) {
	res = &committeeservice.CommitteeFullWithReadonlyAttributes{}
	log.Printf(ctx, "committeeService.update-committee-base")
	return
}

// Delete Committee
func (s *committeeServicesrvc) DeleteCommittee(ctx context.Context, p *committeeservice.DeleteCommitteePayload) (err error) {
	log.Printf(ctx, "committeeService.delete-committee")
	return
}

// Get Committee Settings
func (s *committeeServicesrvc) GetCommitteeSettings(ctx context.Context, p *committeeservice.GetCommitteeSettingsPayload) (res *committeeservice.GetCommitteeSettingsResult, err error) {
	res = &committeeservice.GetCommitteeSettingsResult{}
	log.Printf(ctx, "committeeService.get-committee-settings")
	return
}

// Update Committee Settings
func (s *committeeServicesrvc) UpdateCommitteeSettings(ctx context.Context, p *committeeservice.UpdateCommitteeSettingsPayload) (res *committeeservice.CommitteeSettingsWithReadonlyAttributes, err error) {
	res = &committeeservice.CommitteeSettingsWithReadonlyAttributes{}
	log.Printf(ctx, "committeeService.update-committee-settings")
	return
}

// Check if the service is able to take inbound requests.
func (s *committeeServicesrvc) Readyz(ctx context.Context) (res []byte, err error) {
	log.Printf(ctx, "committeeService.readyz")
	return
}

// Check if the service is alive.
func (s *committeeServicesrvc) Livez(ctx context.Context) (res []byte, err error) {
	log.Printf(ctx, "committeeService.livez")
	return
}
