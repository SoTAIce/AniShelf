import type {CapacitorConfig} from '@capacitor/cli';

const config:CapacitorConfig={
 appId:'io.github.sotaice.anishelf',
 appName:'AniShelf',
 webDir:'dist',
 server:{cleartext:true},
 android:{allowMixedContent:true},
};

export default config;
