package dbus

// Introspection XML for each exported D-Bus object type.
// libsecret calls Introspect() on every object before interacting with it.
// Without this, godbus returns UnknownMethod and libsecret concludes
// "the object does not implement the Secret interface".

const serviceIntrospectXML = `
<node>
  <interface name="org.freedesktop.Secret.Service">
    <method name="OpenSession">
      <arg name="algorithm" type="s"  direction="in"/>
      <arg name="input"     type="v"  direction="in"/>
      <arg name="output"    type="v"  direction="out"/>
      <arg name="result"    type="o"  direction="out"/>
    </method>
    <method name="CreateCollection">
      <arg name="properties" type="a{sv}" direction="in"/>
      <arg name="alias"      type="s"     direction="in"/>
      <arg name="collection" type="o"     direction="out"/>
      <arg name="prompt"     type="o"     direction="out"/>
    </method>
    <method name="SearchItems">
      <arg name="attributes" type="a{ss}" direction="in"/>
      <arg name="unlocked"   type="ao"    direction="out"/>
      <arg name="locked"     type="ao"    direction="out"/>
    </method>
    <method name="Unlock">
      <arg name="objects"  type="ao" direction="in"/>
      <arg name="unlocked" type="ao" direction="out"/>
      <arg name="prompt"   type="o"  direction="out"/>
    </method>
    <method name="Lock">
      <arg name="objects" type="ao" direction="in"/>
      <arg name="locked"  type="ao" direction="out"/>
      <arg name="prompt"  type="o"  direction="out"/>
    </method>
    <method name="GetSecrets">
      <arg name="items"   type="ao"           direction="in"/>
      <arg name="session" type="o"            direction="in"/>
      <arg name="secrets" type="a{o(oayays)}" direction="out"/>
    </method>
    <method name="ReadAlias">
      <arg name="name"       type="s" direction="in"/>
      <arg name="collection" type="o" direction="out"/>
    </method>
    <method name="SetAlias">
      <arg name="name"       type="s" direction="in"/>
      <arg name="collection" type="o" direction="in"/>
    </method>
    <signal name="CollectionCreated"><arg name="collection" type="o"/></signal>
    <signal name="CollectionDeleted"><arg name="collection" type="o"/></signal>
    <signal name="CollectionChanged"><arg name="collection" type="o"/></signal>
    <property name="Collections" type="ao" access="read"/>
  </interface>
</node>`

const collectionIntrospectXML = `
<node>
  <interface name="org.freedesktop.Secret.Collection">
    <method name="CreateItem">
      <arg name="properties" type="a{sv}"    direction="in"/>
      <arg name="secret"     type="(oayays)" direction="in"/>
      <arg name="replace"    type="b"        direction="in"/>
      <arg name="item"       type="o"        direction="out"/>
      <arg name="prompt"     type="o"        direction="out"/>
    </method>
    <method name="SearchItems">
      <arg name="attributes" type="a{ss}" direction="in"/>
      <arg name="results"    type="ao"    direction="out"/>
    </method>
    <method name="Delete">
      <arg name="prompt" type="o" direction="out"/>
    </method>
    <property name="Items"    type="ao" access="read"/>
    <property name="Label"    type="s"  access="readwrite"/>
    <property name="Locked"   type="b"  access="read"/>
    <property name="Created"  type="t"  access="read"/>
    <property name="Modified" type="t"  access="read"/>
    <signal name="ItemCreated"><arg name="item" type="o"/></signal>
    <signal name="ItemDeleted"><arg name="item" type="o"/></signal>
    <signal name="ItemChanged"><arg name="item" type="o"/></signal>
  </interface>
</node>`

const itemIntrospectXML = `
<node>
  <interface name="org.freedesktop.Secret.Item">
    <method name="GetSecret">
      <arg name="session" type="o"        direction="in"/>
      <arg name="secret"  type="(oayays)" direction="out"/>
    </method>
    <method name="SetSecret">
      <arg name="secret" type="(oayays)" direction="in"/>
    </method>
    <method name="Delete">
      <arg name="prompt" type="o" direction="out"/>
    </method>
    <property name="Locked"     type="b"     access="read"/>
    <property name="Attributes" type="a{ss}" access="readwrite"/>
    <property name="Label"      type="s"     access="readwrite"/>
    <property name="Created"    type="t"     access="read"/>
    <property name="Modified"   type="t"     access="read"/>
  </interface>
</node>`

const sessionIntrospectXML = `
<node>
  <interface name="org.freedesktop.Secret.Session">
    <method name="Close"/>
  </interface>
</node>`

const promptIntrospectXML = `
<node>
  <interface name="org.freedesktop.Secret.Prompt">
    <method name="Prompt">
      <arg name="window-id" type="s" direction="in"/>
    </method>
    <method name="Dismiss"/>
    <signal name="Completed">
      <arg name="dismissed" type="b"/>
      <arg name="result"    type="v"/>
    </signal>
  </interface>
</node>`
