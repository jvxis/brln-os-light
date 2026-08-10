package privileged

import (
	"context"
	"testing"
)

type recordingPackages struct {
	ensureCalls int
	statusCalls int
	feature     PackageFeature
	dryRun      bool
	state       PackageFeatureState
	err         error
}

func (packages *recordingPackages) EnsureFeature(_ context.Context, feature PackageFeature, dryRun bool) (PackageFeatureState, error) {
	packages.ensureCalls++
	packages.feature = feature
	packages.dryRun = dryRun
	return packages.state, packages.err
}

func (packages *recordingPackages) FeatureStatus(_ context.Context, feature PackageFeature) (PackageFeatureState, error) {
	packages.statusCalls++
	packages.feature = feature
	return packages.state, packages.err
}

func TestBrokerPackageFeatureUsesTypedManagerAndMutationLock(t *testing.T) {
	packages := &recordingPackages{state: PackageFeatureState{Status: "indexing"}}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Packages = packages
	params, err := MarshalParams(PackageFeatureParams{Feature: PackageFeatureDockerRuntime})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "package_ensure_1", Operation: OperationPackageEnsure, Params: params,
	})
	if !response.OK || packages.ensureCalls != 1 || packages.feature != PackageFeatureDockerRuntime || packages.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("response/packages/locker=%#v/%#v/%#v", response, packages, locker)
	}
}

func TestBrokerPackageFeatureStatusIsReadOnly(t *testing.T) {
	packages := &recordingPackages{state: PackageFeatureState{Status: "indexed"}}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Packages = packages
	params, err := MarshalParams(PackageFeatureParams{Feature: PackageFeatureDockerRuntime})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "package_status_1", Operation: OperationPackageStatus, Params: params,
	})
	if !response.OK || packages.statusCalls != 1 || locker.locks != 0 {
		t.Fatalf("response/packages/locker=%#v/%#v/%#v", response, packages, locker)
	}
}
