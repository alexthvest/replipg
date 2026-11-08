package items

#Rarity: "common" | "uncommon" | "rare" | "epic" | "legendary"

#Item: {
	name!:        string
	description!: string
	rarity!:      #Rarity
	mode!:        "stackable" | "individual"
}

#Items: {
	[string]: #Item
}

#Items
