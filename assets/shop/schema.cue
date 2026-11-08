package shop

#Amount: string & =~"^[1-9][0-9]{0,99}$"

#Offer: close({
	mode!:      "stackable" | "individual"
	buyPrice?:  #Amount
	sellPrice?: #Amount
})

#Offers: {
	[string]: #Offer
}

#Offers
