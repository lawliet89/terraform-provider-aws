// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudfront

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/cloudfront"
	"github.com/hashicorp/aws-sdk-go-base/v2/awsv1shim/v2/tfawserr"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
	"github.com/hashicorp/terraform-provider-aws/internal/errs/sdkdiag"
	"github.com/hashicorp/terraform-provider-aws/internal/tfresource"
)

// @SDKResource("aws_cloudfront_function")
func ResourceFunction() *schema.Resource {
	return &schema.Resource{
		CreateWithoutTimeout: resourceFunctionCreate,
		ReadWithoutTimeout:   resourceFunctionRead,
		UpdateWithoutTimeout: resourceFunctionUpdate,
		DeleteWithoutTimeout: resourceFunctionDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"code": {
				Type:     schema.TypeString,
				Required: true,
			},
			"comment": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"etag": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"live_stage_etag": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"name": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"publish": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  true,
			},
			"runtime": {
				Type:         schema.TypeString,
				Required:     true,
				ValidateFunc: validation.StringInSlice(cloudfront.FunctionRuntime_Values(), false),
			},
			"key_value_store_association": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem: map[string]*schema.Schema{
					"key_value_store_arn": {
						Type:     schema.TypeString,
						Required: true,
					},
				},
			},
			"status": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func resourceFunctionCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).CloudFrontConn(ctx)

	functionName := d.Get("name").(string)
	keyValueAssociations := resourceFunctionExpandKeyValueStoreAssociation(d.Get("key_value_store_association").(*schema.Set).List())
	input := &cloudfront.CreateFunctionInput{
		FunctionCode: []byte(d.Get("code").(string)),
		FunctionConfig: &cloudfront.FunctionConfig{
			Comment:                   aws.String(d.Get("comment").(string)),
			Runtime:                   aws.String(d.Get("runtime").(string)),
			KeyValueStoreAssociations: keyValueAssociations,
		},
		Name: aws.String(functionName),
	}

	log.Printf("[DEBUG] Creating CloudFront Function: %s", functionName)
	output, err := conn.CreateFunctionWithContext(ctx, input)

	if err != nil {
		return sdkdiag.AppendErrorf(diags, "creating CloudFront Function (%s): %s", functionName, err)
	}

	d.SetId(aws.StringValue(output.FunctionSummary.Name))

	if d.Get("publish").(bool) {
		input := &cloudfront.PublishFunctionInput{
			Name:    aws.String(d.Id()),
			IfMatch: output.ETag,
		}

		log.Printf("[DEBUG] Publishing CloudFront Function: %s", input)
		_, err := conn.PublishFunctionWithContext(ctx, input)

		if err != nil {
			return sdkdiag.AppendErrorf(diags, "publishing CloudFront Function (%s): %s", d.Id(), err)
		}
	}

	return append(diags, resourceFunctionRead(ctx, d, meta)...)
}

func resourceFunctionRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).CloudFrontConn(ctx)

	describeFunctionOutput, err := FindFunctionByNameAndStage(ctx, conn, d.Id(), cloudfront.FunctionStageDevelopment)

	if !d.IsNewResource() && tfresource.NotFound(err) {
		log.Printf("[WARN] CloudFront Function (%s) not found, removing from state", d.Id())
		d.SetId("")
		return diags
	}

	if err != nil {
		return sdkdiag.AppendErrorf(diags, "reading CloudFront Function (%s) DEVELOPMENT stage: %s", d.Id(), err)
	}

	d.Set("arn", describeFunctionOutput.FunctionSummary.FunctionMetadata.FunctionARN)
	d.Set("comment", describeFunctionOutput.FunctionSummary.FunctionConfig.Comment)
	d.Set("etag", describeFunctionOutput.ETag)
	d.Set("name", describeFunctionOutput.FunctionSummary.Name)
	d.Set("runtime", describeFunctionOutput.FunctionSummary.FunctionConfig.Runtime)
	d.Set("status", describeFunctionOutput.FunctionSummary.Status)

	if err := d.Set("key_value_store_association", resourceFunctionFlattenKeyValueStoreAssociation(describeFunctionOutput.FunctionSummary.FunctionConfig.KeyValueStoreAssociations)); err != nil {
		return sdkdiag.AppendErrorf(diags, "setting key_value_store_association: %s", err)
	}

	getFunctionOutput, err := conn.GetFunctionWithContext(ctx, &cloudfront.GetFunctionInput{
		Name:  aws.String(d.Id()),
		Stage: aws.String(cloudfront.FunctionStageDevelopment),
	})

	if err != nil {
		return sdkdiag.AppendErrorf(diags, "reading CloudFront Function (%s) DEVELOPMENT stage code: %s", d.Id(), err)
	}

	d.Set("code", string(getFunctionOutput.FunctionCode))

	describeFunctionOutput, err = FindFunctionByNameAndStage(ctx, conn, d.Id(), cloudfront.FunctionStageLive)

	if tfresource.NotFound(err) {
		d.Set("live_stage_etag", "")
	} else if err != nil {
		return sdkdiag.AppendErrorf(diags, "reading CloudFront Function (%s) LIVE stage: %s", d.Id(), err)
	} else {
		d.Set("live_stage_etag", describeFunctionOutput.ETag)
	}

	return diags
}

func resourceFunctionUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).CloudFrontConn(ctx)
	etag := d.Get("etag").(string)

	if d.HasChanges("code", "comment", "runtime", "key_value_store_association") {
		keyValueAssociations := resourceFunctionExpandKeyValueStoreAssociation(d.Get("key_value_store_association").(*schema.Set).List())
		input := &cloudfront.UpdateFunctionInput{
			FunctionCode: []byte(d.Get("code").(string)),
			FunctionConfig: &cloudfront.FunctionConfig{
				Comment:                   aws.String(d.Get("comment").(string)),
				Runtime:                   aws.String(d.Get("runtime").(string)),
				KeyValueStoreAssociations: keyValueAssociations,
			},
			Name:    aws.String(d.Id()),
			IfMatch: aws.String(etag),
		}

		log.Printf("[INFO] Updating CloudFront Function: %s", d.Id())
		output, err := conn.UpdateFunctionWithContext(ctx, input)

		if err != nil {
			return sdkdiag.AppendErrorf(diags, "updating CloudFront Function (%s): %s", d.Id(), err)
		}

		etag = aws.StringValue(output.ETag)
	}

	if d.Get("publish").(bool) {
		input := &cloudfront.PublishFunctionInput{
			Name:    aws.String(d.Id()),
			IfMatch: aws.String(etag),
		}

		log.Printf("[DEBUG] Publishing CloudFront Function: %s", d.Id())
		_, err := conn.PublishFunctionWithContext(ctx, input)

		if err != nil {
			return sdkdiag.AppendErrorf(diags, "publishing CloudFront Function (%s): %s", d.Id(), err)
		}
	}

	return append(diags, resourceFunctionRead(ctx, d, meta)...)
}

func resourceFunctionDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).CloudFrontConn(ctx)

	log.Printf("[INFO] Deleting CloudFront Function: %s", d.Id())
	_, err := conn.DeleteFunctionWithContext(ctx, &cloudfront.DeleteFunctionInput{
		Name:    aws.String(d.Id()),
		IfMatch: aws.String(d.Get("etag").(string)),
	})

	if tfawserr.ErrCodeEquals(err, cloudfront.ErrCodeNoSuchFunctionExists) {
		return diags
	}

	if err != nil {
		return sdkdiag.AppendErrorf(diags, "deleting CloudFront Function (%s): %s", d.Id(), err)
	}

	return diags
}

func resourceFunctionExpandKeyValueStoreAssociation(associations []interface{}) *cloudfront.KeyValueStoreAssociations {
	if len(associations) == 0 {
		return nil
	}
	items := []*cloudfront.KeyValueStoreAssociation{}

	for _, association := range associations {
		item := association.(map[string]interface{})
		items = append(items, &cloudfront.KeyValueStoreAssociation{
			KeyValueStoreARN: aws.String(item["key_value_store_arn"].(string)),
		})
	}

	return &cloudfront.KeyValueStoreAssociations{
		Items:    items,
		Quantity: aws.Int64(int64(len(items))),
	}
}

func resourceFunctionFlattenKeyValueStoreAssociation(associations *cloudfront.KeyValueStoreAssociations) []interface{} {
	if associations == nil {
		return nil
	}
	items := []interface{}{}

	for _, association := range associations.Items {
		items = append(items, map[string]interface{}{
			"key_value_store_arn": aws.StringValue(association.KeyValueStoreARN),
		})
	}

	return items
}
