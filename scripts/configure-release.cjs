const fs = require('node:fs');
const version = process.env.RELEASE_VERSION;
const channel = process.env.RELEASE_CHANNEL;
if (!['rc', 'stable'].includes(channel) || !/^\d+\.\d+\.\d+(?:-rc\.\d+\.\d+)?$/.test(version)) {
  throw new Error('Invalid release version or channel');
}
if ((channel === 'rc') !== version.includes('-rc.')) throw new Error('Version does not match channel');
const [owner, repo] = process.env.GITHUB_REPOSITORY.split('/');
const pkg = JSON.parse(fs.readFileSync('frontend/package.json', 'utf8'));
pkg.version = version;
fs.writeFileSync('frontend/package.json', JSON.stringify(pkg, null, 2) + '\n');
const config = pkg.build;
config.publish = { provider: 'github', owner, repo, channel: channel === 'rc' ? 'rc' : 'latest', releaseType: channel === 'rc' ? 'prerelease' : 'release' };
config.generateUpdatesFilesForAllChannels = false;
// Separate matrix jobs must not overwrite each other's installers.
config.nsis.artifactName = '${productName}-${version}-x64-arm64-win.${ext}';
config.linux.artifactName = '${productName}-${version}-${arch}-linux.${ext}';
fs.writeFileSync('frontend/release-builder.json', JSON.stringify(config, null, 2) + '\n');
