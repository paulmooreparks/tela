export namespace main {
	
	export class ActiveConnection {
	    machine: string;
	    service: string;
	    hubName: string;
	    pid: number;
	    output: string;
	    running: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ActiveConnection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.machine = source["machine"];
	        this.service = source["service"];
	        this.hubName = source["hubName"];
	        this.pid = source["pid"];
	        this.output = source["output"];
	        this.running = source["running"];
	    }
	}
	export class CommandLogEntry {
	    time: string;
	    description: string;
	    command: string;
	
	    static createFrom(source: any = {}) {
	        return new CommandLogEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.description = source["description"];
	        this.command = source["command"];
	    }
	}
	export class ServiceInfo {
	    name: string;
	    port: number;
	    proto: string;
	
	    static createFrom(source: any = {}) {
	        return new ServiceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.port = source["port"];
	        this.proto = source["proto"];
	    }
	}
	export class MachineStatus {
	    id: string;
	    hostname: string;
	    os: string;
	    agentConnected: boolean;
	    sessionCount: number;
	    lastSeen: string;
	    services: ServiceInfo[];
	
	    static createFrom(source: any = {}) {
	        return new MachineStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.hostname = source["hostname"];
	        this.os = source["os"];
	        this.agentConnected = source["agentConnected"];
	        this.sessionCount = source["sessionCount"];
	        this.lastSeen = source["lastSeen"];
	        this.services = this.convertValues(source["services"], ServiceInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class HubStatus {
	    online: boolean;
	    hubName: string;
	    machines: MachineStatus[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new HubStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.online = source["online"];
	        this.hubName = source["hubName"];
	        this.machines = this.convertValues(source["machines"], MachineStatus);
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class KnownHub {
	    name: string;
	    url: string;
	    hasToken: boolean;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new KnownHub(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	        this.hasToken = source["hasToken"];
	        this.source = source["source"];
	    }
	}
	

}

