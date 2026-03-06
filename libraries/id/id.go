package id

import "go.jetify.com/typeid"

// Generate creates a new TypeID with the given prefix.
// The suffix is a UUIDv7 encoding a timestamp for natural ordering.
// Example: id.Generate("message") → "message_01jkx5g8r3e5a7b9c2d4f6h8j0"
func Generate(prefix string) string {
	tid, err := typeid.WithPrefix(prefix)
	if err != nil {
		panic("id.Generate: " + err.Error())
	}
	return tid.String()
}
