package fineup

import (
	"reflect"
	"testing"
)

func TestPlanParts(t *testing.T) {
	tests := []struct {
		name     string
		size     int64
		partSize int64
		want     []Part
		wantErr  bool
	}{
		{
			name:     "smaller than one part",
			size:     10,
			partSize: 100,
			want:     []Part{{Index: 0, Offset: 0, Size: 10}},
		},
		{
			name:     "exactly one part",
			size:     100,
			partSize: 100,
			want:     []Part{{Index: 0, Offset: 0, Size: 100}},
		},
		{
			name:     "one byte over one part",
			size:     101,
			partSize: 100,
			want: []Part{
				{Index: 0, Offset: 0, Size: 100},
				{Index: 1, Offset: 100, Size: 1},
			},
		},
		{
			name:     "exact multiple",
			size:     300,
			partSize: 100,
			want: []Part{
				{Index: 0, Offset: 0, Size: 100},
				{Index: 1, Offset: 100, Size: 100},
				{Index: 2, Offset: 200, Size: 100},
			},
		},
		{
			name:     "ragged tail",
			size:     250,
			partSize: 100,
			want: []Part{
				{Index: 0, Offset: 0, Size: 100},
				{Index: 1, Offset: 100, Size: 100},
				{Index: 2, Offset: 200, Size: 50},
			},
		},
		{
			name:     "part size defaults when zero",
			size:     DefaultPartSize + 1,
			partSize: 0,
			want: []Part{
				{Index: 0, Offset: 0, Size: DefaultPartSize},
				{Index: 1, Offset: DefaultPartSize, Size: 1},
			},
		},
		{
			name:     "part size defaults when negative",
			size:     1,
			partSize: -5,
			want:     []Part{{Index: 0, Offset: 0, Size: 1}},
		},
		{name: "empty file is an error", size: 0, partSize: 100, wantErr: true},
		{name: "negative size is an error", size: -1, partSize: 100, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PlanParts(tt.size, tt.partSize)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("PlanParts(%d, %d) returned no error, want one", tt.size, tt.partSize)
				}
				if got != nil {
					t.Fatalf("PlanParts(%d, %d) returned %v with an error, want nil", tt.size, tt.partSize, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("PlanParts(%d, %d) returned error %v, want none", tt.size, tt.partSize, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("PlanParts(%d, %d) = %v, want %v", tt.size, tt.partSize, got, tt.want)
			}
		})
	}
}

func TestPlanPartsCoversTheWholeFile(t *testing.T) {
	const size = 1_106_621_110 // the size of the file from the captured upload
	parts, err := PlanParts(size, DefaultPartSize)
	if err != nil {
		t.Fatalf("PlanParts returned error %v, want none", err)
	}
	if len(parts) != 222 {
		t.Fatalf("PlanParts produced %d parts, want 222", len(parts))
	}
	var total int64
	for i, p := range parts {
		if p.Index != i {
			t.Fatalf("part %d has Index %d", i, p.Index)
		}
		if p.Offset != total {
			t.Fatalf("part %d starts at %d, want %d", i, p.Offset, total)
		}
		if p.Size <= 0 || p.Size > DefaultPartSize {
			t.Fatalf("part %d has size %d, want between 1 and %d", i, p.Size, DefaultPartSize)
		}
		total += p.Size
	}
	if total != size {
		t.Fatalf("parts cover %d bytes, want %d", total, size)
	}
}

func TestDefaultPartSize(t *testing.T) {
	if DefaultPartSize != 5_000_000 {
		t.Fatalf("DefaultPartSize = %d, want 5000000", DefaultPartSize)
	}
}
