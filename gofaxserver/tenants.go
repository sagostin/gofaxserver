package gofaxserver

type Tenant struct {
	ID         uint           `gorm:"primaryKey" json:"id"`
	Username   string         `gorm:"unique;not null" json:"username"`
	Password   string         `gorm:"not null" json:"password"` // this can also be used for api key for authenticating for web hook integration?
	Address    string         `json:"address"`
	Name       string         `json:"name"`
	LogPrivacy bool           `json:"log_privacy"`
	Numbers    []TenantNumber `gorm:"foreignKey:TenantID" json:"numbers"`
}

type TenantNumber struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	TenantID uint   `gorm:"index;not null" json:"tenant_id"`
	Number   string `gorm:"unique;not null" json:"number"`
	CID      string `json:"cid"`     // caller id that is displayed on the fax? eg. +1 555-555-5555
	Header   string `json:"webhook"` // this is the name displayed at the top of the fax eg. "Company Faxing Relay"
}
