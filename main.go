package main

import (
    "context"
    "fmt"
    "log"
    "net/http"
    "strconv"

    dialogflow "cloud.google.com/go/dialogflow/apiv2"
    "github.com/golang/protobuf/ptypes/struct"
    "google.golang.org/api/option"
    "github.com/gin-gonic/gin"
    dialogflowpb "google.golang.org/genproto/googleapis/cloud/dialogflow/v2"
    "github.com/golang/protobuf/jsonpb"
)

// DialogflowProcessor has all the information for connecting with Dialogflow
type DialogflowProcessor struct {
    projectID        string
    authJSONFilePath string
    lang             string
    timeZone         string
    sessionClient    *dialogflow.SessionsClient
    ctx              context.Context
}

// NLPResponse is the struct for the response
type NLPResponse struct {
    Intent     string            `json:"intent"`
    Confidence float32           `json:"confidence"`
    Entities   map[string]string `json:"entities"`
}

var dp DialogflowProcessor

func main() {

}

// This function's name is a must. App Engine uses it to drive the requests properly.
func init() {
    // projectID, authJSON filepath, language, timezone
    dp.init("dialogflow-go", "dialogflow-go-2728cf6fd9ad.json", "en", "Europe/Amsterdam")
    // Starts a new Gin instance with no middle-ware
    r := gin.New()
    log.Println("Started listening...")
    // Define your handlers
    r.GET("/", func(c *gin.Context) {
        c.String(http.StatusOK, "Hello World!")
    })
    r.GET("/ping", func(c *gin.Context) {
        c.String(http.StatusOK, "pong")
    })
    r.POST("/ping", func(c *gin.Context) {
        c.JSON(200, gin.H{
            "message": "pong",
        })
    })
    // handle Dialogflow at this endpoint
    r.POST("/endpoint", handleEndpoint)
    // fulfillment webhook (from different medium article)
    r.POST("/webhook", handleWebhook)
    // listen and serve on 0.0.0.0:8080
    r.Run()
    // For Google AppEngine - bridges gin and AppEngine
    // Handle all requests using net/http
    http.Handle("/", r)
}

// more complex webhhok which takes raw message and uses NLP
// before returning response
func handleEndpoint(c *gin.Context) {
    // post form not querystring
    message := c.PostForm("message")
    log.Println(message)
    // Use NLP
    response := dp.processNLP(message, "testUser")
    log.Printf("%#v\n", response)
    c.JSON(200, response)
}

// Simple webhook just example so it ain't doing anything useful
func handleWebhook(c *gin.Context) {
    var err error

    wr := dialogflowpb.WebhookRequest{}
    var unmar jsonpb.Unmarshaler  // https://github.com/google/go-genproto/issues/74
    unmar.AllowUnknownFields = true
    if err = unmar.Unmarshal(c.Request.Body, &wr); err != nil {
        log.Println(err.Error())
        c.Status(http.StatusBadRequest)
        return
    }
    log.Println(wr.GetQueryResult().GetQueryText())
    // log.Println(wr)
    
    fullfillment := dialogflowpb.WebhookResponse{
        FulfillmentText: "How the fuck should I know?!",
        Payload: struct{
            Platform: 8,
            RichMessage: {
                "expectUserResponse": true,
                "richResponse": {
                  "items": [
                    {
                      "simpleResponse": {
                        "textToSpeech": "this is a simple response"
                      }
                    }
                  ]
                }
              }
        }
    }
    c.JSON(http.StatusOK, fullfillment)
}

func (dp *DialogflowProcessor) init(a ...string) (err error) {
    dp.projectID = a[0]
    dp.authJSONFilePath = a[1]
    dp.lang = a[2]
    dp.timeZone = a[3]

    // Auth process: https://dialogflow.com/docs/reference/v2-auth-setup

    dp.ctx = context.Background()
    sessionClient, err := dialogflow.NewSessionsClient(dp.ctx, option.WithCredentialsFile(dp.authJSONFilePath))
    if err != nil {
        log.Fatal("Error in auth with Dialogflow")
    }
    dp.sessionClient = sessionClient

    return
}

func (dp *DialogflowProcessor) processNLP(rawMessage string, username string) (r NLPResponse) {
    sessionID := username
    request := dialogflowpb.DetectIntentRequest{
        Session: fmt.Sprintf("projects/%s/agent/sessions/%s", dp.projectID, sessionID),
        QueryInput: &dialogflowpb.QueryInput{
            Input: &dialogflowpb.QueryInput_Text{
                Text: &dialogflowpb.TextInput{
                    Text:         rawMessage,
                    LanguageCode: dp.lang,
                },
            },
        },
        QueryParams: &dialogflowpb.QueryParameters{
            TimeZone: dp.timeZone,
        },
    }
    response, err := dp.sessionClient.DetectIntent(dp.ctx, &request)
    if err != nil {
        log.Fatalf("Error in communication with Dialogflow %s", err.Error())
        return
    }
    queryResult := response.GetQueryResult()
    if queryResult.Intent != nil {
        r.Intent = queryResult.Intent.DisplayName
        r.Confidence = float32(queryResult.IntentDetectionConfidence)
    }
    r.Entities = make(map[string]string)
    params := queryResult.Parameters.GetFields()
    if len(params) > 0 {
        for paramName, p := range params {
            fmt.Printf("Param %s: %s (%s)", paramName, p.GetStringValue(), p.String())
            extractedValue := extractDialogflowEntities(p)
            r.Entities[paramName] = extractedValue
        }
    }
    return
}

func extractDialogflowEntities(p *structpb.Value) (extractedEntity string) {
    kind := p.GetKind()
    switch kind.(type) {
    case *structpb.Value_StringValue:
        return p.GetStringValue()
    case *structpb.Value_NumberValue:
        return strconv.FormatFloat(p.GetNumberValue(), 'f', 6, 64)
    case *structpb.Value_BoolValue:
        return strconv.FormatBool(p.GetBoolValue())
    case *structpb.Value_StructValue:
        s := p.GetStructValue()
        fields := s.GetFields()
        extractedEntity = ""
        for key, value := range fields {
            if key == "amount" {
                extractedEntity = fmt.Sprintf("%s%s", extractedEntity, strconv.FormatFloat(value.GetNumberValue(), 'f', 6, 64))
            }
            if key == "unit" {
                extractedEntity = fmt.Sprintf("%s%s", extractedEntity, value.GetStringValue())
            }
            if key == "date_time" {
                extractedEntity = fmt.Sprintf("%s%s", extractedEntity, value.GetStringValue())
            }
            // @TODO: Other entity types can be added here
        }
        return extractedEntity
    case *structpb.Value_ListValue:
        list := p.GetListValue()
        if len(list.GetValues()) > 1 {
            // @TODO: Extract more values
        }
        extractedEntity = extractDialogflowEntities(list.GetValues()[0])
        return extractedEntity
    default:
        return ""
    }
}
