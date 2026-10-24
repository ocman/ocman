package convevents

// DurabilityOf returns the durability class of ev's type.
func DurabilityOf(ev Event) Durability { return ev.durability() }
