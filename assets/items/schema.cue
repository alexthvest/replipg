package items

#Rarity: "common" | "uncommon" | "rare" | "epic" | "legendary"

#Item: {
	name!:        string
	description!: string
	rarity!:      #Rarity
}

#Items: {
	[string]: #Item
}

#Items
