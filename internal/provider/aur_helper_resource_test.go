package provider

import (
	"context"
	"reflect"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type fakeAURHelperManager struct {
	status  AURHelperStatus
	exists  bool
	ensured []AURHelperSpec
	removed []AURHelperSpec
}

func (m *fakeAURHelperManager) HelperStatus(ctx context.Context, spec AURHelperSpec) (AURHelperStatus, bool, error) {
	return m.status, m.exists, nil
}

func (m *fakeAURHelperManager) EnsureHelper(ctx context.Context, spec AURHelperSpec) (AURHelperStatus, error) {
	m.ensured = append(m.ensured, spec)
	m.status.ReasonUser = true
	return m.status, nil
}

func (m *fakeAURHelperManager) RemoveHelper(ctx context.Context, spec AURHelperSpec) error {
	m.removed = append(m.removed, spec)
	return nil
}

func (m *fakeAURHelperManager) NeedsPrivilegeEscalation() bool {
	return false
}

func TestHydrateAURHelperState(t *testing.T) {
	t.Parallel()

	model := AURHelperResourceModel{}
	hydrateAURHelperState(&model, AURHelperStatus{
		Name:             "yay",
		Package:          "yay-bin",
		Path:             "/usr/bin/yay",
		InstalledVersion: "13.0.1-1",
		ReasonUser:       true,
	})

	if model.ID.ValueString() != "yay" || model.Name.ValueString() != "yay" {
		t.Fatalf("unexpected identity: %#v", model)
	}
	if model.Package.ValueString() != "yay-bin" || model.Path.ValueString() != "/usr/bin/yay" {
		t.Fatalf("unexpected package/path: %#v", model)
	}
	if model.InstalledVersion.ValueString() != "13.0.1-1" {
		t.Fatalf("unexpected version: %#v", model.InstalledVersion)
	}
	if model.InstallReason.ValueString() != packageInstallReasonExplicit {
		t.Fatalf("unexpected install reason: %#v", model.InstallReason)
	}
	if model.DeleteOnDestroy.IsNull() || model.DeleteOnDestroy.ValueBool() {
		t.Fatalf("delete_on_destroy should default false: %#v", model.DeleteOnDestroy)
	}
}

func TestAURHelperModifyPlanDefaultsOmittedPackageFromConfig(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	r := &AURHelperResource{}
	schema := aurHelperResourceTestSchema(t, r)

	configModel := AURHelperResourceModel{
		Name:            types.StringValue("yay"),
		Package:         types.StringNull(),
		InstallReason:   types.StringValue(packageInstallReasonExplicit),
		DeleteOnDestroy: types.BoolValue(false),
	}
	planModel := AURHelperResourceModel{
		ID:               types.StringValue("yay"),
		Name:             types.StringValue("yay"),
		Package:          types.StringValue("yay-bin"),
		Path:             types.StringValue("/usr/bin/yay"),
		InstalledVersion: types.StringValue("13.0.1-1"),
		InstallReason:    types.StringValue(packageInstallReasonExplicit),
		DeleteOnDestroy:  types.BoolValue(false),
	}
	config := aurHelperResourceTestConfig(t, schema, &configModel)
	plan := tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), nil)}
	if diags := plan.Set(ctx, &planModel); diags.HasError() {
		t.Fatalf("encode plan: %v", diags)
	}
	state := tfsdk.State{Schema: schema, Raw: plan.Raw}
	resp := frameworkresource.ModifyPlanResponse{Plan: plan}

	r.ModifyPlan(ctx, frameworkresource.ModifyPlanRequest{Config: config, State: state, Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("modify plan: %v", resp.Diagnostics)
	}
	var got AURHelperResourceModel
	if diags := resp.Plan.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode plan: %v", diags)
	}
	if got.Package.ValueString() != "yay" {
		t.Fatalf("omitted package planned as %q, want name default yay", got.Package.ValueString())
	}
}

func TestAURHelperModifyPlanPreservesUnknownConfiguredPackage(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	r := &AURHelperResource{}
	schema := aurHelperResourceTestSchema(t, r)
	model := AURHelperResourceModel{
		Name:            types.StringValue("yay"),
		Package:         types.StringUnknown(),
		InstallReason:   types.StringValue(packageInstallReasonExplicit),
		DeleteOnDestroy: types.BoolValue(false),
	}
	config := aurHelperResourceTestConfig(t, schema, &model)
	plan := tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), nil)}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatalf("encode plan: %v", diags)
	}
	state := tfsdk.State{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), nil)}
	resp := frameworkresource.ModifyPlanResponse{Plan: plan}

	r.ModifyPlan(ctx, frameworkresource.ModifyPlanRequest{Config: config, State: state, Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("modify plan: %v", resp.Diagnostics)
	}
	var got AURHelperResourceModel
	if diags := resp.Plan.Get(ctx, &got); diags.HasError() {
		t.Fatalf("decode plan: %v", diags)
	}
	if !got.Package.IsUnknown() {
		t.Fatalf("configured unknown package was overwritten: %#v", got.Package)
	}
}

func TestAURHelperInstalledDependencyPlansAndAppliesExplicitReason(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	manager := &fakeAURHelperManager{
		exists: true,
		status: AURHelperStatus{
			Name:             "yay",
			Package:          "yay-bin",
			Path:             "/usr/bin/yay",
			InstalledVersion: "13.0.1-1",
			ReasonUser:       false,
		},
	}
	r := &AURHelperResource{manager: manager}
	schema := aurHelperResourceTestSchema(t, r)

	stateModel := AURHelperResourceModel{DeleteOnDestroy: types.BoolValue(false)}
	hydrateAURHelperState(&stateModel, manager.status)
	if stateModel.InstallReason.ValueString() != packageInstallReasonDependency {
		t.Fatalf("observed reason %q, want dependency", stateModel.InstallReason.ValueString())
	}
	state := tfsdk.State{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), nil)}
	if diags := state.Set(ctx, &stateModel); diags.HasError() {
		t.Fatalf("encode state: %v", diags)
	}
	planModel := stateModel
	planModel.InstallReason = types.StringValue(packageInstallReasonExplicit)
	plan := tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), nil)}
	if diags := plan.Set(ctx, &planModel); diags.HasError() {
		t.Fatalf("encode plan: %v", diags)
	}
	configModel := AURHelperResourceModel{
		Name:            types.StringValue("yay"),
		Package:         types.StringValue("yay-bin"),
		InstallReason:   types.StringValue(packageInstallReasonExplicit),
		DeleteOnDestroy: types.BoolValue(false),
	}
	config := aurHelperResourceTestConfig(t, schema, &configModel)
	modifyResp := frameworkresource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, frameworkresource.ModifyPlanRequest{Config: config, State: state, Plan: plan}, &modifyResp)
	if modifyResp.Diagnostics.HasError() {
		t.Fatalf("modify plan: %v", modifyResp.Diagnostics)
	}

	updateResp := frameworkresource.UpdateResponse{State: tfsdk.State{Schema: schema, Raw: modifyResp.Plan.Raw}}
	r.Update(ctx, frameworkresource.UpdateRequest{State: state, Plan: modifyResp.Plan}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update: %v", updateResp.Diagnostics)
	}
	if want := []AURHelperSpec{{Name: "yay", Package: "yay-bin"}}; !reflect.DeepEqual(manager.ensured, want) {
		t.Fatalf("ensured %#v, want %#v", manager.ensured, want)
	}
	var applied AURHelperResourceModel
	if diags := updateResp.State.Get(ctx, &applied); diags.HasError() {
		t.Fatalf("decode applied state: %v", diags)
	}
	if applied.InstallReason.ValueString() != packageInstallReasonExplicit {
		t.Fatalf("applied reason %q, want explicit", applied.InstallReason.ValueString())
	}
}

func aurHelperResourceTestSchema(t *testing.T, r *AURHelperResource) resourceschema.Schema {
	t.Helper()
	var resp frameworkresource.SchemaResponse
	r.Schema(t.Context(), frameworkresource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("resource schema: %v", resp.Diagnostics)
	}
	return resp.Schema
}

func aurHelperResourceTestConfig(t *testing.T, schema resourceschema.Schema, model *AURHelperResourceModel) tfsdk.Config {
	t.Helper()
	plan := tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(t.Context()), nil)}
	if diags := plan.Set(t.Context(), model); diags.HasError() {
		t.Fatalf("encode config model: %v", diags)
	}
	return tfsdk.Config{Schema: schema, Raw: plan.Raw}
}

func TestAURHelperSpecFromModel(t *testing.T) {
	t.Parallel()

	spec := aurHelperSpecFromModel(AURHelperResourceModel{
		Name:    types.StringValue("paru"),
		Package: types.StringValue("paru-bin"),
	})
	if spec.Name != "paru" || spec.Package != "paru-bin" {
		t.Fatalf("unexpected spec: %#v", spec)
	}
}
