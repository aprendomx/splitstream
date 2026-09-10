package webtls

// Avisador expone avisador para el test de cadencia (ver la desviación anotada en
// webtls_test.go: no se puede probar la cadencia disparando GetCertificate contra el
// propio dominio porque autocert saldría a la red real).
var Avisador = avisador

// AvisoCadaMax expone avisoCadaMax para que el test de cadencia use el mismo valor.
const AvisoCadaMax = avisoCadaMax
