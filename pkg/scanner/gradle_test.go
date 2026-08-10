package scanner

import (
	"reflect"
	"testing"
)

func TestGradleCatalogResolvesVersionRefAndEveryLibraryForm(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "gradle/libs.versions.toml", `[versions]
retrofit = "2.9.0"
kotlin = { require = "1.9.0" }

[libraries]
# module + version.ref, the form the Gradle docs lead with
retrofit = { module = "com.squareup.retrofit2:retrofit", version.ref = "retrofit" }
# separate group and name, with an inline literal version
gson = { group = "com.google.code.gson", name = "gson", version = "2.10.1" }
# the "group:artifact:version" shorthand
junit = "junit:junit:4.13.2"
# a rich version resolved through the ref
stdlib = { module = "org.jetbrains.kotlin:kotlin-stdlib", version.ref = "kotlin" }
# no version at all: the coordinate still carries the graph
bom = { module = "com.acme:bom" }

[bundles]
networking = ["retrofit", "gson"]

[plugins]
android-application = { id = "com.android.application", version = "8.5.0" }
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "gradle/libs.versions.toml", Ecosystem: EcosystemGradle,
		Deps: []DeclaredDep{
			{Name: "com.acme:bom", Kind: KindDependencies},
			{Name: "com.google.code.gson:gson", Range: "2.10.1", Kind: KindDependencies},
			{Name: "com.squareup.retrofit2:retrofit", Range: "2.9.0", Kind: KindDependencies},
			{Name: "junit:junit", Range: "4.13.2", Kind: KindDependencies},
			{Name: "org.jetbrains.kotlin:kotlin-stdlib", Range: "1.9.0", Kind: KindDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestGradleCatalogAliasesCollapseOntoOneDeclaration(t *testing.T) {
	// Two aliases naming the same library at the same version are one
	// declaration; at different versions the file really does declare two.
	dir := t.TempDir()
	write(t, dir, "libs.versions.toml", `[libraries]
core = { module = "com.acme:core", version = "1.0.0" }
core-alias = { module = "com.acme:core", version = "1.0.0" }
core-old = { module = "com.acme:core", version = "0.9.0" }
`)
	m := scanOne(t, dir)
	want := []DeclaredDep{
		{Name: "com.acme:core", Range: "0.9.0", Kind: KindDependencies},
		{Name: "com.acme:core", Range: "1.0.0", Kind: KindDependencies},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps mismatch:\n got %+v\nwant %+v", m.Deps, want)
	}
	if !m.Root {
		t.Error("a catalog in the scanned folder is a root manifest")
	}
}

func TestGradleCatalogSkipsMalformedEntries(t *testing.T) {
	// One odd declaration must not void a manifest, matching how the Cargo,
	// python and pubspec parsers treat an unrecognised value shape.
	dir := t.TempDir()
	write(t, dir, "libs.versions.toml", `[libraries]
good = { module = "com.acme:core", version = "1.0.0" }
no-colon = { module = "nocolon" }
group-only = { group = "com.acme" }
not-a-table = 42
too-many-parts = "a:b:c:d"
`)
	m := scanOne(t, dir)
	want := []DeclaredDep{{Name: "com.acme:core", Range: "1.0.0", Kind: KindDependencies}}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps mismatch:\n got %+v\nwant %+v", m.Deps, want)
	}
}

func TestGradleBuildBothDialects(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "build.gradle", `plugins { id 'com.android.application' }

android {
    namespace 'com.acme.lib'
    defaultConfig {
        applicationId "com.acme.app"
        versionCode 42
        versionName "1.2.3"
        testInstrumentationRunner 'androidx.test.runner.AndroidJUnitRunner'
    }
    productFlavors {
        free { versionName "1.2.3-free" }
    }
}

dependencies {
    implementation 'androidx.core:core-ktx:1.12.0'
    api "com.squareup.retrofit2:retrofit:2.9.0"
    compileOnly 'javax.annotation:javax.annotation-api:1.3.2'
    testImplementation 'junit:junit:4.13.2'
    androidTestImplementation 'androidx.test.ext:junit:1.1.5'
    kapt 'com.google.dagger:dagger-compiler:2.48'
    implementation project(':core')
    compileOnly project(path: ':libs:annotations')
}
`)
	m := scanOne(t, dir)
	want := Manifest{
		Path: "build.gradle", Ecosystem: EcosystemGradle,
		Name: "com.acme.app", Version: "1.2.3", BuildNumber: "42", Root: true,
		Deps: []DeclaredDep{
			{Name: "androidx.core:core-ktx", Range: "1.12.0", Kind: KindDependencies},
			{Name: "com.squareup.retrofit2:retrofit", Range: "2.9.0", Kind: KindDependencies},
			{Name: "core", Kind: KindDependencies},
			{Name: "androidx.test.ext:junit", Range: "1.1.5", Kind: KindDevDependencies},
			{Name: "com.google.dagger:dagger-compiler", Range: "2.48", Kind: KindDevDependencies},
			{Name: "junit:junit", Range: "4.13.2", Kind: KindDevDependencies},
			{Name: "annotations", Kind: KindPeerDependencies},
			{Name: "javax.annotation:javax.annotation-api", Range: "1.3.2", Kind: KindPeerDependencies},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest mismatch:\n got %+v\nwant %+v", m, want)
	}
}

func TestGradleBuildKotlinDialect(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "build.gradle.kts", `android {
    namespace = "com.example.android.architecture.blueprints.todoapp"
    defaultConfig {
        applicationId = "com.example.android.architecture.blueprints.main"
        versionCode = 1
        versionName = "1.0"
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.12.0")
    implementation(project(":core"))
    testImplementation(libs.junit)
}
`)
	m := scanOne(t, dir)
	if m.Name != "com.example.android.architecture.blueprints.main" || m.Version != "1.0" || m.BuildNumber != "1" {
		t.Errorf("identity mismatch: %+v", m)
	}
	want := []DeclaredDep{
		{Name: "androidx.core:core-ktx", Range: "1.12.0", Kind: KindDependencies},
		{Name: "core", Kind: KindDependencies},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps mismatch:\n got %+v\nwant %+v", m.Deps, want)
	}
}

func TestGradleBuildDropsWhatItCannotResolve(t *testing.T) {
	// Every line here comes from a real Android project. None of them can be
	// resolved from this file alone, so none of them may be guessed at.
	dir := t.TempDir()
	write(t, dir, "build.gradle", `buildscript {
    dependencies {
        classpath 'com.android.tools.build:gradle:8.5.0'
    }
}

android {
    defaultConfig {
        applicationId "org.wordpress.android"
        versionName project.findProperty("prototypeBuildVersionName") ?: versionProperties.getProperty("versionName")
        versionCode versionProperties.getProperty("versionCode").toInteger()
    }
}

dependencies {
    implementation(libs.androidx.navigation.compose)
    implementation "androidx.core:core-ktx:$coreVersion"
    implementation("$gradle.ext.gravatarBinaryPath:${libs.versions.gravatar.get()}")
    // implementation 'commented:out:1.0'
    implementation 'androidx.core:core-ktx:1.12.0' /* the real one */
}
`)
	m := scanOne(t, dir)
	if m.Name != "org.wordpress.android" {
		t.Errorf("name = %q", m.Name)
	}
	if m.Version != "" || m.BuildNumber != "" {
		t.Errorf("a computed version is not a literal: %+v", m)
	}
	want := []DeclaredDep{{Name: "androidx.core:core-ktx", Range: "1.12.0", Kind: KindDependencies}}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps mismatch:\n got %+v\nwant %+v", m.Deps, want)
	}
}

func TestGradleBuildBlockCommentSpansLines(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "build.gradle", `dependencies {
    /* this whole block is out:
    implementation 'ghost:ghost:1.0'
    */
    implementation 'real:real:1.0'
}
`)
	m := scanOne(t, dir)
	want := []DeclaredDep{{Name: "real:real", Range: "1.0", Kind: KindDependencies}}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("deps mismatch:\n got %+v\nwant %+v", m.Deps, want)
	}
}
