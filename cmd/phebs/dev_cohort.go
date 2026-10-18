package main

import (
	"errors"

	"github.com/bmeddeb/phebs/internal/analysisunit"
)

type devCohort string

const (
	devCohortFocused devCohort = "focused"
	devCohortWhole   devCohort = "whole"
)

// An absent selector preserves the original explicit T30.7 fixture binding.
// The Make recipes select whole explicitly; an explicit empty value refuses.
func parseDevCohort(value string, present bool) (devCohort, error) {
	if !present {
		return devCohortFocused, nil
	}
	cohort := devCohort(value)
	if err := cohort.validate(); err != nil {
		return "", err
	}
	return cohort, nil
}

func (cohort devCohort) validate() error {
	if cohort != devCohortFocused && cohort != devCohortWhole {
		return errors.New("PHEBS_DEV_COHORT must be exactly whole or focused; an explicit empty value is invalid")
	}
	return nil
}

func t307DemoAnalysisUnit() analysisunit.Config {
	return analysisunit.Config{
		Name:    "orders-service",
		Primary: []string{"service/orders"},
		Supporting: []string{
			"api/orders.proto",
			"gen/ordersv1/orders_grpc.pb.go",
			"generated-from-snapshot.json",
			"go.mod",
		},
	}
}
