//go:build !darwin

package dispatchadmission

import "os"

func captureInheritedProductionWorkspace() *ProductionWorkspaceBinding { return nil }

func DescribeProductionWorkspace(*os.File, string) (ProductionWorkspaceBinding, error) {
	return ProductionWorkspaceBinding{}, ErrProductionBootstrap
}

func adoptProductionWorkspace(*os.File, ProductionWorkspaceBinding) (*productionWorkspace, error) {
	return nil, ErrProductionBootstrap
}
