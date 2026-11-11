package store

import "testing"

func TestPersonImages(t *testing.T) {
	db := newTestDB(t)

	id := PersonID("Tom Hanks")
	if id != PersonID("  tom hanks ") {
		t.Fatal("person id should be case/space insensitive")
	}
	if len(id) != 32 {
		t.Fatalf("id length = %d, want 32", len(id))
	}

	if err := db.UpsertPersonImages([]PersonImage{
		{ID: id, Name: "Tom Hanks", ImageURL: "https://example.com/tom.jpg"},
		{ID: PersonID("无头像"), Name: "无头像"},
	}); err != nil {
		t.Fatal(err)
	}

	byID, err := db.PersonImageByID(id)
	if err != nil || byID.Name != "Tom Hanks" || byID.ImageURL == "" {
		t.Fatalf("by id = %+v, %v", byID, err)
	}
	byName, err := db.PersonImageByName("Tom Hanks")
	if err != nil || byName.ID != id {
		t.Fatalf("by name = %+v, %v", byName, err)
	}
	if _, err := db.PersonImageByID("missing"); err != ErrNotFound {
		t.Fatalf("missing err = %v", err)
	}
}
