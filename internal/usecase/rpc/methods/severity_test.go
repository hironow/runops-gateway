package methods_test

import (
	"testing"

	"github.com/hironow/runops-gateway/internal/usecase/rpc/methods"
)

func TestClassify_KnownLowSeverity(t *testing.T) {
	cases := []string{
		methods.MethodNameProjectGet,
		methods.MethodNameProjectList,
		methods.MethodNamePendingGet,
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			sev, ok := methods.Classify(name)
			if !ok {
				t.Fatalf("Classify(%q): ok=false, want true", name)
			}
			if sev != methods.SeverityLow {
				t.Errorf("Classify(%q): sev=%q, want %q", name, sev, methods.SeverityLow)
			}
		})
	}
}

func TestClassify_KnownHighSeverity(t *testing.T) {
	cases := []string{
		methods.MethodNameProjectAdd,
		methods.MethodNameProjectArchive,
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			sev, ok := methods.Classify(name)
			if !ok {
				t.Fatalf("Classify(%q): ok=false, want true", name)
			}
			if sev != methods.SeverityHigh {
				t.Errorf("Classify(%q): sev=%q, want %q", name, sev, methods.SeverityHigh)
			}
		})
	}
}

func TestClassify_UnknownMethod_ReturnsNotOK(t *testing.T) {
	sev, ok := methods.Classify("runops.admin.project.unknown")
	if ok {
		t.Errorf("Classify(unknown): ok=true, want false")
	}
	if sev != "" {
		t.Errorf("Classify(unknown): sev=%q, want empty", sev)
	}
}

func TestListBySeverity_AllRegisteredMethodsClassified(t *testing.T) {
	// Every method name registered in method_names.go must appear in the
	// classifier. This guards against new methods being added without
	// declaring their severity.
	all := []string{
		methods.MethodNameProjectGet,
		methods.MethodNameProjectList,
		methods.MethodNamePendingGet,
		methods.MethodNameProjectAdd,
		methods.MethodNameProjectArchive,
	}
	high := methods.ListBySeverity(methods.SeverityHigh)
	low := methods.ListBySeverity(methods.SeverityLow)
	total := len(high) + len(low)
	if total != len(all) {
		t.Errorf("classified total: got %d (high=%d + low=%d), want %d",
			total, len(high), len(low), len(all))
	}
}

func TestSeverityConstants_NonEmpty(t *testing.T) {
	if methods.SeverityHigh == "" {
		t.Error("SeverityHigh must be non-empty")
	}
	if methods.SeverityLow == "" {
		t.Error("SeverityLow must be non-empty")
	}
	if methods.SeverityHigh == methods.SeverityLow {
		t.Error("SeverityHigh and SeverityLow must differ")
	}
}
