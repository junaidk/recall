Listen Wortschatz Goethe-Zertifikat
Sie können sich die Listen zu den jeweiligen Sprachstufen zur Erreichung der Goethe-Zertifikate im JSON-Format herunterladen. Das DWDS bietet 3 Listen an, die Struktur der JSON-Daten finden Sie in der nachfolgenden Tabelle dokumentiert:

Wortschatz für das Goethe-Zertifikat A1 als CSV
Wortschatz für das Goethe-Zertifikat A1 als JSON
Wortschatz für das Goethe-Zertifikat A2 als CSV
Wortschatz für das Goethe-Zertifikat A2 als JSON
Wortschatz für das Goethe-Zertifikat B1 als CSV
Wortschatz für das Goethe-Zertifikat B1 als JSON
Die CSV-Dateien sind derart gegliedert, dass es für jede gültige Schreibung eines Wortes bzw. Ausdrucks eine separate Zeile mit allen im DWDS-Wörterbuch dazu vorhandenen Angaben gibt. Sind bei einem Eintrag mehrere Genera bzw. bestimmte Artikel möglich, werden diese durch Komma getrennt. Beispielauszug:

"Lemma","URL","Wortart","Genus","Artikel","nur_im_Plural"
"abschließen","https://www.dwds.de/wb/abschlie%C3%9Fen","Verb","","","0"
"Ahnung","https://www.dwds.de/wb/Ahnung","Substantiv","fem.","die","0"
"Leute","https://www.dwds.de/wb/Leute","Substantiv","","","1"
"Teil","https://www.dwds.de/wb/Teil","Substantiv","mask., neutr.","der, das","0"
Die Struktur der JSON-Daten finden Sie in der nachfolgenden Tabelle dokumentiert:

articles	optional, bei Nomen: Liste mit entsprechenden bestimmten Artikeln (der, die, das)
genera	optional: Liste der zum Lemma gehörigen Genera (mask., fem., neutr.)
onlypl	optional: fester Wert nur im Plural, falls ein Wort nur im Plural verwendet werden kann
pos	Wortart, siehe Wortarten im DWDS
sch	Liste mit Schreibungen bzw. Formen im Wörterbuchartikel
sch / lemma	Schreibung des Lemmas
sch / hidx	optional: Homographenindex (bei mehreren Wörterbucheinträgen wie ¹Bank und ²Bank)
url	kanonische URL zum zugehörigen Wörterbuchartikel
