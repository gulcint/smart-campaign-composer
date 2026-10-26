package seed

import "strings"

// Curated common Turkish given names and surnames, combined at random; no combination targets
// a real person (REQ-SEED-02). Every given name is at most 20 characters.
var firstNames = []string{
	"Ahmet", "Mehmet", "Mustafa", "Ali", "Hüseyin", "Hasan", "İbrahim", "İsmail", "Osman", "Yusuf",
	"Murat", "Ömer", "Emre", "Burak", "Can", "Cem", "Kerem", "Barış", "Serkan", "Oğuz",
	"Ayşe", "Fatma", "Emine", "Hatice", "Zeynep", "Elif", "Meryem", "Şerife", "Zehra", "Sultan",
	"Merve", "Büşra", "Esra", "Gizem", "Özge", "Derya", "Ebru", "Gül", "Çiğdem", "Selin",
	"Deniz", "Ece", "İrem", "Tuğba", "Şule", "Nur", "Aslı", "Damla", "Yağmur", "Ilgın",
}

var lastNames = []string{
	"Yılmaz", "Kaya", "Demir", "Şahin", "Çelik", "Yıldız", "Yıldırım", "Öztürk", "Aydın", "Özdemir",
	"Arslan", "Doğan", "Kılıç", "Aslan", "Çetin", "Kara", "Koç", "Kurt", "Özkan", "Şimşek",
	"Polat", "Öz", "Korkmaz", "Erdoğan", "Yavuz", "Can", "Acar", "Şen", "Aktaş", "Güler",
	"Yalçın", "Güneş", "Bozkurt", "Bulut", "Keskin", "Ünal", "Turan", "Gül", "Özer", "Işık",
}

var otherCities = []string{"Ankara", "İzmir", "Bursa", "Antalya", "Adana", "Konya", "Gaziantep", "Kayseri", "Eskişehir"}

var productCategories = []string{"electronics", "fashion", "home", "beauty", "sports", "books", "toys", "grocery"}

const (
	istanbul        = "İstanbul"
	longFirstName   = "Şükriye Gülizar Çağla Ayşegül İpek Öykü Nilüfer Su" // exactly 50 characters
	sharedPhone     = "+905000009999"
	sharedEmail     = "shared.contact@example.test"
	sharedPhoneSlot = 9999
)

var asciiFold = strings.NewReplacer(
	"ş", "s", "Ş", "s", "ğ", "g", "Ğ", "g", "ı", "i", "İ", "i", "I", "i",
	"ö", "o", "Ö", "o", "ü", "u", "Ü", "u", "ç", "c", "Ç", "c", "â", "a", "î", "i", "û", "u",
)

// emailLocal lowercases and ASCII-folds a name and keeps only [a-z0-9].
func emailLocal(name string) string {
	folded := strings.ToLower(asciiFold.Replace(name))
	var b strings.Builder
	for _, r := range folded {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
