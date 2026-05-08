module.exports = {
  branches: ["main"],
  tagFormat: "v${version}",
  repositoryUrl: "https://github.com/oleg-tkachuk/paladin.git",
  plugins: [
    ["@semantic-release/commit-analyzer", { preset: "conventionalcommits" }],
    "@semantic-release/release-notes-generator",
    ["@semantic-release/github", { successComment: false, failComment: false }]
  ],
};
