package db

import (
	"encoding/json"
	"testing"

	"github.com/neouks/series/allianceprofile"
)

func setField(t *testing.T, d *allianceprofile.Document, sectionID, fieldID, value string) {
	t.Helper()
	for si := range d.Sections {
		if d.Sections[si].ID != sectionID {
			continue
		}
		for fi := range d.Sections[si].Fields {
			if d.Sections[si].Fields[fi].ID == fieldID {
				d.Sections[si].Fields[fi].Value = value
				d.Sections[si].Fields[fi].Status = allianceprofile.StatusSupplied
				return
			}
		}
	}
	t.Fatalf("field %s/%s not found", sectionID, fieldID)
}

// The legacy PATCH endpoint must not copy flat columns into an existing
// portrait. S1-03/S1-04 are not semantically equivalent to owner_unit/business,
// and doing so polluted the ledger with long profile text.
func TestAllianceMergeLegacyDocumentKeepsExistingDocument(t *testing.T) {
	doc := allianceprofile.Template()
	setField(t, &doc, "phase-1", "S1-03", "对接人：安全部；停止联系人：值班")
	setField(t, &doc, "phase-1", "S1-04", "用户查询；首轮目标：验证注入")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := allianceMergeLegacyDocument(string(raw), AllianceSystemInput{
		OwnerUnit: "台账归属单位",
		Business:  "台账业务描述",
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("existing document must not be reported as changed")
	}
	if string(got) != string(raw) {
		t.Fatalf("document was overwritten:\nwant %s\ngot  %s", raw, got)
	}
}

func TestAllianceMergeLegacyDocumentBuildsDefaultForEmpty(t *testing.T) {
	got, changed, err := allianceMergeLegacyDocument("{}", AllianceSystemInput{
		Name: "系统 A", Environment: "测试", OwnerUnit: "归属单位",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("empty document should be initialized")
	}
	var doc allianceprofile.Document
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) == 0 {
		t.Fatal("default document has no sections")
	}
}

// The task-detail UI treats snapshot as an object (AllianceSnapshotDocument),
// not as a JSON string. Ensure the API contract stays object-shaped.
func TestTaskAllianceSnapshotJSONIsObject(t *testing.T) {
	snap := TaskAllianceSnapshot{
		SystemID:       3,
		SystemName:     "系统 A",
		ProfileVersion: 4,
		Snapshot:       json.RawMessage(`{"id":3,"name":"系统 A","environment":"测试","version":4,"document":{"sections":[]}}`),
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["snapshot"].(map[string]any); !ok {
		t.Fatalf("snapshot must be a JSON object, got %T: %s", body["snapshot"], raw)
	}
}
