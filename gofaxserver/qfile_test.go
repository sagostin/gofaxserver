package gofaxserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOpenQfile(t *testing.T) {
	assert := assert.New(t)
	file, err := OpenQfile("testdata/qfile")

	// Make sure it implements Qfiler
	var qfiler Qfiler = file
	assert.NotNil(qfiler)

	// Loading successful?
	assert.NoError(err)
	assert.NotNil(file)
	assert.Len(file.params, 71)

	// Existing string value
	assert.Equal("04012345678", file.GetString("number"))
	assert.EqualValues([]string{"04012345678"}, file.GetAll("number"))

	// Non-existing string value
	assert.Nil(file.GetAll("non-existing"))
	assert.Equal("", file.GetString("non-existing"))

	// Existing int value
	i, err := file.GetInt("priority")
	assert.Equal(127, i)
	assert.NoError(err)

	// Existing non-int value
	i, err = file.GetInt("sender")
	assert.Equal(0, i)
	assert.Error(err)

	// Non-existing int value
	i, err = file.GetInt("xxxxxx")
	assert.Equal(0, i)
	assert.EqualError(err, "tag not found")

	// Set new value
	file.Add("foo", "bar")
	assert.Equal("bar", file.GetString("foo"))
	file.Set("foo", "baz")
	assert.Equal("baz", file.GetString("foo"))

	file.Set("baz", "foo")
	assert.Equal("baz", file.GetString("foo"))

	// Close
	assert.NoError(file.Close())
}
