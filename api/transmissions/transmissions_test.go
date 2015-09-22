package transmissions

import (
	"testing"

	"github.com/SparkPost/go-sparkpost/api"
	_ "github.com/SparkPost/go-sparkpost/api/templates"
	"github.com/SparkPost/go-sparkpost/test"
)

func TestTransmissions(t *testing.T) {
	cfgMap, err := test.LoadConfig()
	if err != nil {
		t.Error(err)
		return
	}
	cfg, err := api.NewConfig(cfgMap)
	if err != nil {
		t.Error(err)
		return
	}

	TransAPI, err := New(*cfg)
	if err != nil {
		t.Error(err)
		return
	}

	T := &Transmission{
		CampaignID: "msys_smoke",
		Recipients: map[string]string{
			"list_id": "test list",
		},
		Content: map[string]string{
			"template_id": "test content",
		},
	}
	err = T.Validate()
	if err != nil {
		t.Error(err)
		return
	}

	id, err := TransAPI.Create(T)
	if err != nil {
		t.Error(err)
		return
	}

	t.Errorf("Transmission created with id [%s]", id)
}
