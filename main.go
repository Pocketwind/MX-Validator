package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Pocketwind/MX-Validator/fsutil"
	"github.com/antchfx/xmlquery"
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

type Message struct {
	Type   string
	Scheme *xsd.Schema
}

func main() {
	fileChannel := make(chan string)
	inputDir := "./input"
	bicData := "./data/bic.csv"
	currencyData := "./data/currency.csv"
	countryData := "./data/country.csv"

	err := fsutil.EnsureDir(inputDir)
	if err != nil {
		fmt.Println("Error ensuring input directory:", err)
		return
	}

	//Currency 리스트
	currencies, err := readColumnByName(currencyData, "Code")
	if err != nil {
		fmt.Println("Error reading currency data:", err)
		return
	}
	//fmt.Println("Loaded currencies:", currencies)

	//Country 리스트
	countries, err := readColumnByName(countryData, "Code")
	if err != nil {
		fmt.Println("Error reading country data:", err)
		return
	}
	//fmt.Println("Loaded countries:", countries)

	//BIC 리스트
	bic11, err := readColumnByName(bicData, "BIC11")
	if err != nil {
		fmt.Println("Error reading BIC data:", err)
		return
	}
	//fmt.Println("Loaded BIC11 codes:", bic11)
	bic8, err := readColumnByName(bicData, "BIC8")
	if err != nil {
		fmt.Println("Error reading BIC data:", err)
		return
	}
	//fmt.Println("Loaded BIC8 codes:", bic8)

	//xsd 스키마 로드
	xsds, err := os.ReadDir("./xsd")
	if err != nil {
		fmt.Println("Error reading XSD directory:", err)
		return
	}
	paths := make([]string, 0, len(xsds))
	for _, entry := range xsds {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".xsd" {
			continue
		}
		paths = append(paths, filepath.Join("./xsd", entry.Name()))
	}
	//struct
	messageTypes := make([]Message, 0)
	for _, path := range paths {
		schema, err := xsd.LoadFile(path, xsd.Options{})
		if err != nil {
			fmt.Println("Error loading schema from file", path, ":", err)
			continue
		}
		messageTypes = append(messageTypes, Message{
			Type:   strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
			Scheme: schema,
		})
	}

	go func() {
		err := fsutil.WatchService(inputDir, fileChannel)
		if err != nil {
			fmt.Println("Error watching service:", err)
			return
		}
	}()

	for file := range fileChannel {
		fmt.Println("File detected:", file)
		var valid bool
		var err error
		valid, err = ValidateFile(file, messageTypes)
		if err != nil {
			fmt.Println("Validation error for file", file, ":", err)
		} else if valid {
			fmt.Println("File", file, "is valid.")
		} else {
			fmt.Println("File", file, "is invalid.")
		}
	}
}

func ValidateFile(path string, messageTypes []Message) (bool, error) {
	apphdr, body, err := ExtractMessageType(path)
	if err != nil {
		fmt.Println("message type extraction error:", err)
		return false, err
	}

	//전문 타입 검색
	fmt.Println("AppHdr type:", apphdr)
	fmt.Println("Message type:", body)

	//Apphdr 검증
	var appHdrSchema *xsd.Schema
	for _, message := range messageTypes {
		if message.Type == apphdr {
			appHdrSchema = message.Scheme
			break
		}
	}
	if appHdrSchema == nil {
		return false, fmt.Errorf("no matching schema found for AppHdr type: %s", apphdr)
	}

	//body 검증
	var bodySchema *xsd.Schema
	for _, message := range messageTypes {
		if message.Type == body {
			// 해당 XSD 스키마를 사용하여 검증 로직 추가
			bodySchema = message.Scheme
			break
		}
	}
	if bodySchema == nil {
		return false, fmt.Errorf("no matching schema found for message type: %s", body)
	}

	// xsd.Validator는 xdm.Node를 입력으로 받으므로 XML을 다시 xdm 트리로 파싱합니다.
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	tree, err := xdm.Parse(file, xdm.ParseOptions{
		TrackPositions: true,
	})
	if err != nil {
		return false, err
	}

	appHdrNode, err := findElement(tree.Root, "AppHdr")
	if err != nil {
		return false, err
	}

	documentNode, err := findElement(tree.Root, "Document")
	if err != nil {
		return false, err
	}

	// AppHdr 검증
	err = appHdrSchema.Validate(appHdrNode, xsd.ValidateOptions{})
	if err != nil {
		return false, err
	}

	// body 검증
	err = bodySchema.Validate(documentNode, xsd.ValidateOptions{})
	if err != nil {
		return false, err
	}
	return true, nil
}

func findElement(node *xdm.Node, localName string) (*xdm.Node, error) {
	if node.Kind == xdm.KindElement && node.Name.Local == localName {
		return node, nil
	}

	for _, child := range node.ChildElements() {
		found, err := findElement(child, localName)
		if err == nil {
			return found, nil
		}
	}

	return nil, fmt.Errorf("%s element not found", localName)
}

func ExtractMessageType(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	doc, err := xmlquery.Parse(file)
	if err != nil {
		return "", "", err
	}

	//cbpr 버전 체크
	cbprVersionNode := xmlquery.FindOne(doc, "//*[local-name()='BizSvc']")
	if cbprVersionNode == nil {
		return "", "", fmt.Errorf("BizSvc element not found")
	}
	cbprVersion := cbprVersionNode.InnerText()

	const iso20022Prefix = "urn:iso:std:iso:20022:tech:xsd:"

	//Apphdr
	apphdr := xmlquery.FindOne(doc, "//*[local-name()='AppHdr']")
	if apphdr == nil {
		return "", "", fmt.Errorf("AppHdr element not found")
	}

	appHdrNamespaceURI := apphdr.NamespaceURI

	if !strings.HasPrefix(appHdrNamespaceURI, iso20022Prefix) {
		return "", "", fmt.Errorf("unsupported namespace in AppHdr: %s", appHdrNamespaceURI)
	}

	//Documents
	documents := xmlquery.FindOne(doc, "//*[local-name()='Document']")
	if documents == nil {
		return "", "", fmt.Errorf("Document element not found")
	}

	bodyNamespaceURI := documents.NamespaceURI

	if !strings.HasPrefix(bodyNamespaceURI, iso20022Prefix) {
		return "", "", fmt.Errorf("unsupported namespace: %s", bodyNamespaceURI)
	}

	messageType := strings.TrimPrefix(bodyNamespaceURI, iso20022Prefix)
	//+cbpr
	if cbprVersion != "" {
		messageType = messageType + "-" + cbprVersion
	}
	appHdrType := strings.TrimPrefix(appHdrNamespaceURI, iso20022Prefix)
	return appHdrType, messageType, nil
}

func readColumnByName(path, columnName string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)

	header, err := reader.Read()
	if err != nil {
		return nil, err
	}

	columnIndex := -1
	for i, name := range header {
		if name == columnName {
			columnIndex = i
			break
		}
	}

	if columnIndex == -1 {
		return nil, fmt.Errorf("column not found: %s", columnName)
	}

	var values []string
	for {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		if columnIndex < len(row) {
			values = append(values, row[columnIndex])
		}
	}

	return values, nil
}
