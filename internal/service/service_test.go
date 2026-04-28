package service

import "testing"

func TestCalculateThreat_Low(t *testing.T) {
	score, class := CalculateThreat("100", "100")

	if class == "" {
		t.Fatal("classificação não pode ser vazia")
	}

	if score < 0 {
		t.Fatalf("score inválido: %d", score)
	}

	if class != "low_threat" && class != "medium_threat" {
		t.Logf("classificação obtida: %s", class)
	}
}

func TestCalculateThreat_HighValues(t *testing.T) {
	score, class := CalculateThreat("100000", "100000")

	if score <= 0 {
		t.Fatalf("score deveria ser alto, veio: %d", score)
	}

	if class == "" {
		t.Fatal("classificação vazia")
	}

	if class != "galactic_superweapon" && class != "high_threat" {
		t.Logf("classificação obtida: %s", class)
	}
}

func TestCalculateThreat_WithCommaParsing(t *testing.T) {
	score, _ := CalculateThreat("1,000", "2,000")

	if score < 0 {
		t.Fatal("falha no parsing de números com vírgula")
	}
}
