package knowledgevault

import "testing"

func TestReaderSearchIncludesArchivesWhenRequested(t *testing.T) {
	config, index, ctx := exportSeedVault(t)
	if _, err := index.Rebuild(ctx, config.VaultPath); err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReader(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	withArchives, err := reader.Search(ctx, DocumentSearchOptions{
		Query: "AHA1 归档内容", Limit: 10, IncludeArchives: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(withArchives.Results) != 1 || !withArchives.Results[0].Archive || withArchives.Results[0].Scope != "archive" {
		t.Fatalf("archive-inclusive search = %#v", withArchives.Results)
	}

	withoutArchives, err := reader.Search(ctx, DocumentSearchOptions{
		Query: "AHA1 归档内容", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutArchives.Results) != 0 {
		t.Fatalf("default search leaked archive results: %#v", withoutArchives.Results)
	}
}
