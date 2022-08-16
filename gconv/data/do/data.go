package do

import "time"

type Item struct {
	Id        int
	Name      string
	CreatedAt time.Time
}

type Data struct {
	Id        int32
	id2       int
	Name      string
	Item      []*Item
	CreatedAt *time.Time
}
