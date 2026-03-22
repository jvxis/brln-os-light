package lndclient

import "testing"

func TestBuildOpenChannelPreviewSelectsSingleInputWithChange(t *testing.T) {
	preview := buildOpenChannelPreview(OpenChannelPreview{
		LocalFundingSat: 100_000,
		SatPerVbyte:     2,
	}, []OnchainUtxo{
		{AmountSat: 200_000, AddressType: "p2wkh"},
	})

	if !preview.EnoughFunds {
		t.Fatalf("expected preview to have enough funds, got %#v", preview)
	}
	if preview.SelectedInputCount != 1 {
		t.Fatalf("expected 1 selected input, got %d", preview.SelectedInputCount)
	}
	if !preview.HasChange {
		t.Fatalf("expected preview to keep a change output")
	}
	if preview.FeeSat <= 0 {
		t.Fatalf("expected fee to be positive, got %d", preview.FeeSat)
	}
	if preview.TotalDebitSat != preview.LocalFundingSat+preview.FeeSat {
		t.Fatalf("expected total debit to match funding + fee, got %d", preview.TotalDebitSat)
	}
}

func TestBuildOpenChannelPreviewAggregatesMultipleInputs(t *testing.T) {
	preview := buildOpenChannelPreview(OpenChannelPreview{
		LocalFundingSat: 140_000,
		SatPerVbyte:     2,
	}, []OnchainUtxo{
		{AmountSat: 80_000, AddressType: "p2wkh"},
		{AmountSat: 75_000, AddressType: "p2wkh"},
	})

	if !preview.EnoughFunds {
		t.Fatalf("expected preview to have enough funds, got %#v", preview)
	}
	if preview.SelectedInputCount != 2 {
		t.Fatalf("expected 2 selected inputs, got %d", preview.SelectedInputCount)
	}
	if preview.SelectedInputSat != 155_000 {
		t.Fatalf("expected selected input sum 155000, got %d", preview.SelectedInputSat)
	}
}

func TestBuildOpenChannelPreviewReportsInsufficientBalance(t *testing.T) {
	preview := buildOpenChannelPreview(OpenChannelPreview{
		LocalFundingSat: 300_000,
		SatPerVbyte:     2,
	}, []OnchainUtxo{
		{AmountSat: 120_000, AddressType: "p2wkh"},
		{AmountSat: 70_000, AddressType: "p2wkh"},
	})

	if preview.EnoughFunds {
		t.Fatalf("expected preview to be insufficient, got %#v", preview)
	}
	if preview.Message == "" {
		t.Fatalf("expected insufficiency message, got empty")
	}
	if preview.EstimatedVbytes <= 0 || preview.FeeSat <= 0 {
		t.Fatalf("expected reference estimate even when insufficient, got %#v", preview)
	}
	if !preview.ReferenceOnly {
		t.Fatalf("expected insufficient preview to be marked as reference only")
	}
}

func TestBuildOpenChannelPreviewAbsorbsDustIntoFeeWhenNeeded(t *testing.T) {
	inputs := []previewInput{{addressType: "p2wkh"}}
	noChangeFeeFloor := estimatePreviewVirtualSize(inputs, openChannelPreviewOutputs(false))
	localFunding := int64(200_000) - noChangeFeeFloor - 40

	preview := buildOpenChannelPreview(OpenChannelPreview{
		LocalFundingSat: localFunding,
		SatPerVbyte:     1,
	}, []OnchainUtxo{
		{AmountSat: 200_000, AddressType: "p2wkh"},
	})

	if !preview.EnoughFunds {
		t.Fatalf("expected preview to have enough funds, got %#v", preview)
	}
	if preview.HasChange {
		t.Fatalf("expected dust remainder to remove change output")
	}
	if preview.FeeSat <= noChangeFeeFloor {
		t.Fatalf("expected final fee %d to exceed floor %d after dust absorption", preview.FeeSat, noChangeFeeFloor)
	}
	if preview.Message == "" {
		t.Fatalf("expected dust absorption message, got empty")
	}
}

func TestBuildOpenChannelPreviewKeepsReferenceEstimateWithNoUtxos(t *testing.T) {
	preview := buildOpenChannelPreview(OpenChannelPreview{
		LocalFundingSat: 500_000,
		SatPerVbyte:     3,
	}, nil)

	if preview.EstimatedVbytes <= 0 {
		t.Fatalf("expected reference vbytes estimate, got %#v", preview)
	}
	if preview.FeeSat <= 0 {
		t.Fatalf("expected reference fee estimate, got %#v", preview)
	}
	if !preview.ReferenceOnly {
		t.Fatalf("expected preview to be marked as reference only")
	}
	if preview.EnoughFunds {
		t.Fatalf("expected preview without utxos to be insufficient")
	}
}
